package updates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var (
	shaPattern          = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	checksumPattern     = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	versionPattern      = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:[-+][0-9A-Za-z.-]+)?$`)
	patchReleasePattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.\*$`)
)

const maxLocalChangelogSize = 1 << 20

const defaultRequestTimeout = 15 * time.Second

var (
	ErrReleaseAllowlistMissing   = errors.New("release allowlist is not configured")
	ErrReleaseAllowlistMalformed = errors.New("release allowlist contains a malformed entry")
)

type Current struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuiltAt   string `json:"builtAt"`
	GoVersion string `json:"goVersion,omitempty"`
}
type Release struct {
	Version           string `json:"version"`
	Commit            string `json:"commit"`
	PublishedAt       string `json:"publishedAt"`
	Changelog         string `json:"changelog"`
	ChangelogSource   string `json:"changelogSource,omitempty"`
	URL               string `json:"url"`
	MigrationRequired bool   `json:"migrationRequired"`
	Verified          bool   `json:"verified"`
	Compatible        bool   `json:"compatible"`
	Checksum          string `json:"checksum"`
	ArtifactName      string `json:"artifactName"`
	ArtifactURL       string `json:"artifactUrl"`
}
type Snapshot struct {
	Current     Current `json:"current"`
	Repository  string  `json:"repository"`
	Branch      string  `json:"branch"`
	Provider    string  `json:"provider"`
	Release     Release `json:"release"`
	Status      string  `json:"status"`
	Installable bool    `json:"installable"`
	Reason      string  `json:"reason,omitempty"`
}

type githubRelease struct {
	TagName         string        `json:"tag_name"`
	Body            string        `json:"body"`
	HTMLURL         string        `json:"html_url"`
	PublishedAt     string        `json:"published_at"`
	TargetCommitish string        `json:"target_commitish"`
	Draft           bool          `json:"draft"`
	Prerelease      bool          `json:"prerelease"`
	Assets          []githubAsset `json:"assets"`
}
type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
}
type githubCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Verification struct {
			Verified bool `json:"verified"`
		} `json:"verification"`
	} `json:"commit"`
}

type githubComparison struct {
	Status string `json:"status"`
}

// githubAPIError deliberately stores only the operation and HTTP metadata.
// Response bodies are not retained because they may contain credentials or
// other provider-controlled data that must never reach the UI or run logs.
type githubAPIError struct {
	Operation  string
	StatusCode int
	Status     string
}

func (e *githubAPIError) Error() string {
	return fmt.Sprintf("%s returned HTTP %d", e.Operation, e.StatusCode)
}

func (r *githubRelease) UnmarshalJSON(b []byte) error {
	var v struct {
		TagName         string        `json:"tag_name"`
		Body            string        `json:"body"`
		HTMLURL         string        `json:"html_url"`
		PublishedAt     string        `json:"published_at"`
		TargetCommitish string        `json:"target_commitish"`
		Draft           bool          `json:"draft"`
		Prerelease      bool          `json:"prerelease"`
		Assets          []githubAsset `json:"assets"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	r.TagName, r.Body, r.HTMLURL, r.PublishedAt, r.TargetCommitish = v.TagName, v.Body, v.HTMLURL, v.PublishedAt, v.TargetCommitish
	r.Draft, r.Prerelease, r.Assets = v.Draft, v.Prerelease, v.Assets
	return nil
}

func ValidateRelease(r Release, repository string) error {
	if !versionPattern.MatchString(r.Version) {
		return errors.New("release version is not a semantic version")
	}
	if !shaPattern.MatchString(r.Commit) {
		return errors.New("release commit is not a full SHA-1")
	}
	if !checksumPattern.MatchString(r.Checksum) {
		return errors.New("release checksum is not a SHA-256")
	}
	if !r.Verified {
		return errors.New("release is not cryptographically verified")
	}
	if !r.Compatible {
		return errors.New("release is not compatible")
	}
	u := strings.TrimSpace(r.URL)
	if !strings.HasPrefix(u, "https://github.com/"+repository+"/releases/") {
		return errors.New("release URL is outside the configured repository")
	}
	return nil
}

func ValidateArtifactURL(artifactURL, repository string) error {
	u, err := url.Parse(strings.TrimSpace(artifactURL))
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil {
		return errors.New("artifact URL is not a GitHub HTTPS URL")
	}
	prefix := "/" + strings.Trim(repository, "/") + "/releases/download/"
	if !strings.HasPrefix(u.Path, prefix) || strings.TrimPrefix(u.Path, prefix) == "" {
		return errors.New("artifact URL is outside the configured repository")
	}
	return nil
}

// ValidateReleaseForBranch verifies the complete release trust chain. The
// branch and the commit resolved from the immutable tag are inputs from the
// GitHub API, never values supplied by the operator or the UI.
func ValidateReleaseForBranch(r Release, repository, branch, approvedBranch, tagCommit string, tagVerified bool) error {
	if strings.TrimSpace(branch) == "" || strings.TrimSpace(approvedBranch) == "" || branch != approvedBranch {
		return errors.New("release branch is not configured")
	}
	if err := ValidateRelease(r, repository); err != nil {
		return err
	}
	if !shaPattern.MatchString(tagCommit) || !strings.EqualFold(r.Commit, tagCommit) {
		return errors.New("release tag does not resolve to the release commit")
	}
	if !tagVerified {
		return errors.New("release tag commit is not cryptographically verified")
	}
	return nil
}

// approvedReleaseTarget accepts the configured branch name or a full commit
// SHA. The SHA form is required when the release workflow binds the tag with
// --target <built-sha> instead of a moving branch HEAD. Other branch names
// fail closed so an unapproved ref cannot become a stable update source.
func approvedReleaseTarget(targetCommitish, branch string) bool {
	target := strings.TrimSpace(targetCommitish)
	if target == "" || strings.TrimSpace(branch) == "" {
		return false
	}
	if target == branch {
		return true
	}
	return shaPattern.MatchString(target)
}

// releaseTargetMatchesTag keeps branch-name targets as a compatibility path
// for older releases and requires a SHA target to be the immutable tag
// commit. Branch membership is still checked separately via compare.
func releaseTargetMatchesTag(targetCommitish, branch, tagCommit string) bool {
	target := strings.TrimSpace(targetCommitish)
	if target == branch && strings.TrimSpace(branch) != "" {
		return true
	}
	return shaPattern.MatchString(target) && shaPattern.MatchString(tagCommit) && strings.EqualFold(target, tagCommit)
}

func Compare(current, release string) string {
	if release == "" {
		return "unavailable"
	}
	if current == release {
		return "up_to_date"
	}
	cm, cok := semver(current)
	rm, rok := semver(release)
	if !cok || !rok {
		return "unverified"
	}
	for i := range cm {
		if rm[i] > cm[i] {
			return "update_available"
		}
		if rm[i] < cm[i] {
			return "up_to_date"
		}
	}
	return "up_to_date"
}
func semver(v string) ([3]int, bool) {
	var out [3]int
	m := versionPattern.FindStringSubmatch(v)
	if m == nil {
		return out, false
	}
	for i := 0; i < 3; i++ {
		var n int
		if _, err := fmt.Sscanf(m[i+1], "%d", &n); err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

type Client struct {
	HTTP               *http.Client
	BaseURL, Token     string
	GOOS, GOARCH       string
	ApprovedTags       []string
	LocalChangelogPath string
	RequestTimeout     time.Duration
}

// LoadLocalChangelog reads an operator-selected Markdown file or the first
// conventional changelog file in the current working directory. It is only a
// content source; it never supplies release trust or installation metadata.
func LoadLocalChangelog(configuredPath string) (content, path string, err error) {
	candidates := []string{strings.TrimSpace(configuredPath)}
	if candidates[0] == "" {
		candidates = []string{"CHANGELOG.md", "CHANGELOG.markdown"}
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		info, statErr := os.Stat(candidate)
		if statErr != nil {
			if os.IsNotExist(statErr) && strings.TrimSpace(configuredPath) == "" {
				continue
			}
			return "", candidate, statErr
		}
		if !info.Mode().IsRegular() {
			return "", candidate, errors.New("changelog path is not a regular file")
		}
		if info.Size() > maxLocalChangelogSize {
			return "", candidate, fmt.Errorf("changelog exceeds %d byte limit", maxLocalChangelogSize)
		}
		file, openErr := os.Open(candidate)
		if openErr != nil {
			return "", candidate, openErr
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maxLocalChangelogSize+1))
		closeErr := file.Close()
		if readErr != nil {
			return "", candidate, readErr
		}
		if closeErr != nil {
			return "", candidate, closeErr
		}
		if len(data) > maxLocalChangelogSize {
			return "", candidate, fmt.Errorf("changelog exceeds %d byte limit", maxLocalChangelogSize)
		}
		return string(data), candidate, nil
	}
	return "", "", nil
}

func tagApproved(tag string, approved []string) bool {
	for _, candidate := range approved {
		candidate = strings.TrimSpace(candidate)
		if candidate == tag {
			return true
		}
		// The release workflow increments the patch component. A patch-only
		// prefix (for example v0.1.*) permits future stable patch releases
		// without turning the trust policy into an unrestricted wildcard.
		if patchReleasePattern.MatchString(candidate) && versionPattern.MatchString(tag) && strings.HasPrefix(tag, strings.TrimSuffix(candidate, "*")) {
			return true
		}
	}
	return false
}

// ValidateReleaseAllowlist validates the explicit release trust policy before
// any network request is made. Exact semantic-version tags and patch-only
// prefixes are supported; empty or malformed policies fail closed.
func ValidateReleaseAllowlist(approved []string) error {
	valid := 0
	for _, candidate := range approved {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		valid++
		if versionPattern.MatchString(candidate) || patchReleasePattern.MatchString(candidate) {
			continue
		}
		return fmt.Errorf("%w: %s", ErrReleaseAllowlistMalformed, candidate)
	}
	if valid == 0 {
		return ErrReleaseAllowlistMissing
	}
	return nil
}

// DownloadAndVerify downloads only the URL selected from the trusted GitHub
// release response and compares the complete byte stream with its advertised
// SHA-256 digest. It deliberately returns no partial artifact on failure.
func (c Client) DownloadAndVerify(ctx context.Context, artifactURL, expected string) ([]byte, error) {
	if !strings.HasPrefix(artifactURL, "https://") || !checksumPattern.MatchString(expected) {
		return nil, errors.New("artifact URL or checksum is not trusted")
	}
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	requestCtx, cancel := c.withRequestTimeout(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, artifactURL, nil)
	if err != nil {
		return nil, err
	}
	c.authorize(req)
	req.Header.Set("Accept", "application/octet-stream")
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artifact download returned %s", res.Status)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 512<<20))
	if err != nil {
		return nil, err
	}
	if len(body) == 512<<20 {
		return nil, errors.New("artifact exceeds maximum size")
	}
	actual := sha256.Sum256(body)
	if !strings.EqualFold(hex.EncodeToString(actual[:]), expected) {
		return nil, errors.New("artifact checksum mismatch")
	}
	return body, nil
}

func (c Client) latest(ctx context.Context, repo string) (githubRelease, error) {
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	requestCtx, cancel := c.withRequestTimeout(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, base+"/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return githubRelease{}, err
	}
	c.authorize(req)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Shipyard-Updates")
	res, err := c.httpClient().Do(req)
	if err != nil {
		return githubRelease{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return githubRelease{}, &githubAPIError{Operation: "GitHub release lookup", StatusCode: res.StatusCode, Status: res.Status}
	}
	var out githubRelease
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}
func (c Client) tagCommit(ctx context.Context, repo, tag string) (githubCommit, error) {
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	requestCtx, cancel := c.withRequestTimeout(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, base+"/repos/"+repo+"/commits/"+tag, nil)
	if err != nil {
		return githubCommit{}, err
	}
	c.authorize(req)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Shipyard-Updates")
	res, err := c.httpClient().Do(req)
	if err != nil {
		return githubCommit{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return githubCommit{}, &githubAPIError{Operation: "GitHub tag lookup", StatusCode: res.StatusCode, Status: res.Status}
	}
	var out githubCommit
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

func (c Client) branchContains(ctx context.Context, repo, branch, commit string) (bool, error) {
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	requestCtx, cancel := c.withRequestTimeout(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, base+"/repos/"+repo+"/compare/"+url.PathEscape(branch)+"..."+url.PathEscape(commit), nil)
	if err != nil {
		return false, err
	}
	c.authorize(req)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Shipyard-Updates")
	res, err := c.httpClient().Do(req)
	if err != nil {
		return false, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return false, &githubAPIError{Operation: "GitHub branch comparison", StatusCode: res.StatusCode, Status: res.Status}
	}
	var comparison githubComparison
	if err := json.NewDecoder(res.Body).Decode(&comparison); err != nil {
		return false, err
	}
	return comparison.Status == "identical" || comparison.Status == "behind", nil
}

func (c Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c Client) withRequestTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := c.RequestTimeout
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

// authorize shares the configured credential across release metadata and
// artifact requests, but never sends it to an arbitrary HTTPS destination.
// Release artifacts are required to come from github.com before installation.
func (c Client) authorize(req *http.Request) {
	if strings.TrimSpace(c.Token) == "" || req.URL == nil {
		return
	}
	host := strings.ToLower(req.URL.Hostname())
	if host == "api.github.com" || host == "github.com" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
}

func Resolve(ctx context.Context, current Current, repo, branch string, client Client) Snapshot {
	if client.HTTP == nil {
		client.HTTP = http.DefaultClient
	}
	s := Snapshot{Current: current, Repository: repo, Branch: branch, Provider: "GitHub", Status: "unavailable"}
	if content, path, err := LoadLocalChangelog(client.LocalChangelogPath); err == nil && content != "" {
		s.Release.Changelog = content
		s.Release.ChangelogSource = "Lokale Markdown-Datei: " + filepath.Base(path)
	} else if err != nil {
		s.Reason = "Lokaler Changelog konnte nicht gelesen werden."
	}
	if err := ValidateReleaseAllowlist(client.ApprovedTags); err != nil {
		s.Status = "unverified"
		s.Reason = releasePolicyReason(err)
		return s
	}
	r, err := client.latest(ctx, repo)
	if err != nil {
		s.Reason = githubErrorReason(err, client.Token)
		return s
	}
	if r.Draft || r.Prerelease || !approvedReleaseTarget(r.TargetCommitish, branch) {
		s.Status, s.Reason = "unverified", "Release ist kein freigegebenes stabiles Release auf dem Zielbranch."
		return s
	}
	if !tagApproved(r.TagName, client.ApprovedTags) {
		s.Status, s.Reason = "unverified", "Release-Tag ist nicht in der konfigurierten Freigabe-Allowlist."
		return s
	}
	commit, err := client.tagCommit(ctx, repo, r.TagName)
	if err != nil {
		s.Status, s.Reason = "unverified", githubErrorReason(err, client.Token)
		return s
	}
	if !releaseTargetMatchesTag(r.TargetCommitish, branch, commit.SHA) {
		s.Status, s.Reason = "unverified", "Release ist kein freigegebenes stabiles Release auf dem Zielbranch."
		return s
	}
	contained, err := client.branchContains(ctx, repo, branch, commit.SHA)
	if err != nil || !contained {
		s.Status = "unverified"
		if err != nil {
			s.Reason = githubErrorReason(err, client.Token)
		} else {
			s.Reason = "Release-Commit gehört nicht nachweislich zum freigegebenen Zielbranch."
		}
		return s
	}
	changelog, changelogSource := r.Body, "GitHub Release"
	if strings.TrimSpace(changelog) == "" {
		changelog, changelogSource = s.Release.Changelog, s.Release.ChangelogSource
	}
	s.Release = Release{Version: r.TagName, Commit: commit.SHA, PublishedAt: r.PublishedAt, Changelog: changelog, ChangelogSource: changelogSource, URL: r.HTMLURL}
	if len(r.Assets) == 0 {
		s.Status, s.Reason = "unverified", "Release enthält kein prüfbares Artefakt."
		return s
	}
	// GitHub's asset digest is the only checksum accepted here; caller must not
	// substitute a user-provided or merely non-empty digest.
	goos, goarch := client.GOOS, client.GOARCH
	if goos == "" || goarch == "" {
		goos, goarch = runtime.GOOS, runtime.GOARCH
	}
	for _, a := range r.Assets {
		name := strings.ToLower(a.Name)
		if strings.HasPrefix(a.Digest, "sha256:") && strings.Contains(name, goos) && strings.Contains(name, goarch) && ValidateArtifactURL(a.BrowserDownloadURL, repo) == nil {
			s.Release.Checksum = strings.TrimPrefix(a.Digest, "sha256:")
			s.Release.ArtifactName, s.Release.ArtifactURL = a.Name, a.BrowserDownloadURL
			break
		}
	}
	if _, err := hex.DecodeString(s.Release.Checksum); err != nil {
		s.Status, s.Reason = "unverified", "Release-Artefakt besitzt keine gültige SHA-256-Prüfsumme."
		return s
	}
	s.Release.Verified = shaPattern.MatchString(s.Release.Commit) && commit.Commit.Verification.Verified
	s.Release.Compatible = s.Release.Verified
	s.Status = Compare(current.Version, s.Release.Version)
	s.Installable = s.Status == "update_available" && ValidateReleaseForBranch(s.Release, repo, branch, branch, commit.SHA, commit.Commit.Verification.Verified) == nil
	if !s.Installable && s.Status == "update_available" {
		s.Status = "unverified"
		s.Reason = "Release-Verifikation ist unvollständig."
	}
	return s
}

func releasePolicyReason(err error) string {
	switch {
	case errors.Is(err, ErrReleaseAllowlistMissing):
		return "Release-Allowlist ist nicht konfiguriert. Setze TASKBOARD_GITHUB_RELEASE_ALLOWLIST."
	case errors.Is(err, ErrReleaseAllowlistMalformed):
		return "Release-Allowlist enthält einen ungültigen Eintrag; erlaubt sind vollständige Tags oder Patch-Präfixe wie v0.1.*."
	default:
		return "Release-Freigabe-Policy konnte nicht sicher geprüft werden."
	}
}

func githubErrorReason(err error, token string) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "Die GitHub-Release-Prüfung ist wegen einer Zeitüberschreitung fehlgeschlagen. Prüfe die Netzwerkverbindung und versuche es erneut."
	}
	if errors.Is(err, context.Canceled) {
		return "Die GitHub-Release-Prüfung wurde abgebrochen. Versuche es erneut."
	}
	var apiErr *githubAPIError
	if !errors.As(err, &apiErr) {
		return "GitHub-Release konnte nicht sicher geprüft werden. Prüfe Netzwerk und GitHub-Konfiguration."
	}
	switch apiErr.StatusCode {
	case http.StatusUnauthorized:
		return "GitHub-Zugriffstoken ist ungültig oder abgelaufen. Prüfe TASKBOARD_GITHUB_TOKEN."
	case http.StatusForbidden:
		return "GitHub-API-Zugriff verweigert oder Rate-Limit erreicht. Prüfe Token-Berechtigungen und versuche es später erneut."
	case http.StatusTooManyRequests:
		return "GitHub-API-Rate-Limit erreicht. Prüfe Token-Berechtigungen und versuche es später erneut."
	case http.StatusNotFound:
		return "GitHub-Repository oder Release wurde nicht gefunden. Prüfe Repository- und Branch-Konfiguration."
	default:
		return "GitHub-Release konnte nicht sicher geprüft werden. Prüfe Netzwerk und GitHub-Konfiguration."
	}
}
