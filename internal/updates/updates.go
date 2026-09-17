package updates

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

var (
	shaPattern      = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	checksumPattern = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)
	versionPattern  = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:[-+][0-9A-Za-z.-]+)?$`)
)

type Current struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	BuiltAt string `json:"builtAt"`
}
type Release struct {
	Version           string `json:"version"`
	Commit            string `json:"commit"`
	PublishedAt       string `json:"publishedAt"`
	Changelog         string `json:"changelog"`
	URL               string `json:"url"`
	MigrationRequired bool   `json:"migrationRequired"`
	Verified          bool   `json:"verified"`
	Compatible        bool   `json:"compatible"`
	Checksum          string `json:"checksum"`
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
	HTTP           *http.Client
	BaseURL, Token string
}

func (c Client) latest(ctx context.Context, repo string) (githubRelease, error) {
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Shipyard-Updates")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return githubRelease{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return githubRelease{}, fmt.Errorf("GitHub release lookup returned %s", res.Status)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/repos/"+repo+"/commits/"+tag, nil)
	if err != nil {
		return githubCommit{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Shipyard-Updates")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return githubCommit{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return githubCommit{}, fmt.Errorf("GitHub tag lookup returned %s", res.Status)
	}
	var out githubCommit
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

func Resolve(ctx context.Context, current Current, repo, branch string, client Client) Snapshot {
	if client.HTTP == nil {
		client.HTTP = http.DefaultClient
	}
	s := Snapshot{Current: current, Repository: repo, Branch: branch, Provider: "GitHub", Status: "unavailable"}
	r, err := client.latest(ctx, repo)
	if err != nil {
		s.Reason = "GitHub-Release konnte nicht sicher geprüft werden."
		return s
	}
	if r.Draft || r.Prerelease || r.TargetCommitish != "" && r.TargetCommitish != branch {
		s.Status, s.Reason = "unverified", "Release ist kein freigegebenes stabiles Release auf dem Zielbranch."
		return s
	}
	commit, err := client.tagCommit(ctx, repo, r.TagName)
	if err != nil {
		s.Status, s.Reason = "unverified", "Release-Tag konnte nicht auf einen Commit aufgelöst werden."
		return s
	}
	s.Release = Release{Version: r.TagName, Commit: commit.SHA, PublishedAt: r.PublishedAt, Changelog: r.Body, URL: r.HTMLURL}
	if len(r.Assets) == 0 {
		s.Status, s.Reason = "unverified", "Release enthält kein prüfbares Artefakt."
		return s
	}
	// GitHub's asset digest is the only checksum accepted here; caller must not
	// substitute a user-provided or merely non-empty digest.
	for _, a := range r.Assets {
		if strings.HasPrefix(a.Digest, "sha256:") {
			s.Release.Checksum = strings.TrimPrefix(a.Digest, "sha256:")
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
	s.Installable = s.Status == "update_available" && ValidateRelease(s.Release, repo) == nil
	if !s.Installable && s.Status == "update_available" {
		s.Status = "unverified"
		s.Reason = "Release-Verifikation ist unvollständig."
	}
	return s
}
