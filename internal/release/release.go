// Package release publishes an accepted Shipyard delivery as one GitHub PR.
// It deliberately has no merge, release, deployment, or force-push operation.
package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strings"
)

var shaPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
var branchPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

type Request struct {
	TaskID, ProjectID, RepositoryURL, SourcePath, SourceBranch, TargetBranch string
	CommitSHA, DiffSummary, Tests, ReviewNotes                               string
	DoneApproved                                                             bool
}

type PushInput struct {
	RepositoryPath, Remote, Branch, CommitSHA string
	Force                                     bool
}
type Pusher interface {
	Push(context.Context, PushInput) error
}

type PullRequest struct {
	Number                int
	URL, Head, Base, Body string
}
type PullRequestInput struct{ Repository, Title, Head, Base, Body string }
type GitHub interface {
	FindPR(context.Context, string, string, string, string, string) (*PullRequest, error)
	CreatePR(context.Context, PullRequestInput) (PullRequest, error)
	UpdatePR(context.Context, int, PullRequestInput) (PullRequest, error)
}
type Result struct {
	PR      PullRequest
	Marker  string
	Updated bool
}

func (r Request) validate() error {
	if !r.DoneApproved {
		return errors.New("release requires an explicit Done approval")
	}
	for name, value := range map[string]string{"task": r.TaskID, "project": r.ProjectID, "source branch": r.SourceBranch, "target branch": r.TargetBranch, "commit": r.CommitSHA} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("missing %s identity", name)
		}
	}
	if strings.TrimSpace(r.SourcePath) == "" {
		return errors.New("missing source repository path")
	}
	if !shaPattern.MatchString(r.CommitSHA) {
		return errors.New("accepted commit must be a full SHA-1")
	}
	if !branchPattern.MatchString(r.SourceBranch) || strings.Contains(r.SourceBranch, "..") {
		return errors.New("invalid source branch")
	}
	if !branchPattern.MatchString(r.TargetBranch) || strings.Contains(r.TargetBranch, "..") {
		return errors.New("invalid target branch")
	}
	return nil
}

func Publish(ctx context.Context, request Request, pusher Pusher, github GitHub) (Result, error) {
	if err := request.validate(); err != nil {
		return Result{}, err
	}
	if pusher == nil || github == nil {
		return Result{}, errors.New("release adapters are not configured")
	}
	owner, repo, err := githubRepository(request.RepositoryURL)
	if err != nil {
		return Result{}, err
	}
	marker := "<!-- shipyard-release-task:" + request.TaskID + " -->"
	push := PushInput{RepositoryPath: request.SourcePath, Remote: "origin", Branch: request.SourceBranch, CommitSHA: request.CommitSHA}
	if err := pusher.Push(ctx, push); err != nil {
		return Result{}, fmt.Errorf("accepted branch push failed: %w", err)
	}
	body := prBody(request, marker)
	in := PullRequestInput{Repository: owner + "/" + repo, Title: "Shipyard: " + request.TaskID, Head: request.SourceBranch, Base: request.TargetBranch, Body: body}
	existing, err := github.FindPR(ctx, owner, repo, request.SourceBranch, request.TargetBranch, marker)
	if err != nil {
		return Result{}, fmt.Errorf("matching PR lookup failed: %w", err)
	}
	if existing != nil {
		if existing.Number <= 0 {
			return Result{}, errors.New("matching PR has no valid number")
		}
		pr, err := github.UpdatePR(ctx, existing.Number, in)
		if err != nil {
			return Result{}, fmt.Errorf("matching PR update failed: %w", err)
		}
		if err := validatePR(pr); err != nil {
			return Result{}, err
		}
		return Result{PR: pr, Marker: marker, Updated: true}, nil
	}
	pr, err := github.CreatePR(ctx, in)
	if err != nil {
		return Result{}, fmt.Errorf("PR creation failed: %w", err)
	}
	if err := validatePR(pr); err != nil {
		return Result{}, err
	}
	return Result{PR: pr, Marker: marker}, nil
}

func validatePR(pr PullRequest) error {
	if pr.Number <= 0 || pr.URL == "" {
		return errors.New("GitHub returned an incomplete PR reference")
	}
	return nil
}

func githubRepository(raw string) (string, string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || strings.ToLower(u.Host) != "github.com" || u.User != nil {
		return "", "", errors.New("repository must be an HTTPS GitHub URL")
	}
	parts := strings.Split(strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", errors.New("repository URL must identify one GitHub repository")
	}
	return parts[0], parts[1], nil
}

func prBody(r Request, marker string) string {
	return strings.Join([]string{marker, "## Shipyard task", r.TaskID, "", "- Project: `" + r.ProjectID + "`", "- Source branch: `" + r.SourceBranch + "`", "- Target branch: `" + r.TargetBranch + "`", "- Accepted commit: `" + r.CommitSHA + "`", "", "## Change summary", r.DiffSummary, "", "## Tests", r.Tests, "", "## Review notes", r.ReviewNotes}, "\n")
}

type GitPusher struct{}

func (GitPusher) Push(ctx context.Context, in PushInput) error {
	if in.Force {
		return errors.New("force-push is forbidden")
	}
	if in.Remote == "" {
		return errors.New("push remote is required")
	}
	cmd := exec.CommandContext(ctx, "git", "-C", in.RepositoryPath, "push", in.Remote, in.CommitSHA+":refs/heads/"+in.Branch)
	if _, err := cmd.CombinedOutput(); err != nil {
		// Git may echo remote URLs or credential-helper diagnostics. Never pass
		// raw command output to an audit/comment channel.
		return errors.New("git push failed")
	}
	return nil
}

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}
type githubPR struct {
	Number  int    `json:"number"`
	HTMLURL string `json:"html_url"`
	Body    string `json:"body"`
	State   string `json:"state"`
	Head    struct {
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

func (c Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}
func (c Client) endpoint(path string) (string, error) {
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	if !strings.HasPrefix(base, "https://") {
		return "", errors.New("GitHub API must use HTTPS")
	}
	return base + path, nil
}
func (c Client) request(ctx context.Context, method, path string, body any, out any) error {
	if c.Token == "" {
		return errors.New("GitHub token is not configured")
	}
	endpoint, err := c.endpoint(path)
	if err != nil {
		return err
	}
	var reader *strings.Reader
	if body == nil {
		reader = strings.NewReader("")
	} else {
		data, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			return marshalErr
		}
		reader = strings.NewReader(string(data))
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return errors.New("GitHub request could not be created")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return errors.New("GitHub request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GitHub returned HTTP %d", resp.StatusCode)
	}
	if out != nil && json.NewDecoder(resp.Body).Decode(out) != nil {
		return errors.New("GitHub returned invalid JSON")
	}
	return nil
}
func (c Client) FindPR(ctx context.Context, owner, repo, head, base, marker string) (*PullRequest, error) {
	var prs []githubPR
	path := "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/pulls?state=open&head=" + url.QueryEscape(owner+":"+head) + "&base=" + url.QueryEscape(base)
	if err := c.request(ctx, http.MethodGet, path, nil, &prs); err != nil {
		return nil, err
	}
	var found *PullRequest
	for _, pr := range prs {
		if strings.Contains(pr.Body, marker) {
			if found != nil {
				return nil, errors.New("multiple matching open PRs found")
			}
			found = &PullRequest{Number: pr.Number, URL: pr.HTMLURL, Head: pr.Head.Ref, Base: pr.Base.Ref, Body: pr.Body}
		}
	}
	return found, nil
}
func (c Client) CreatePR(ctx context.Context, in PullRequestInput) (PullRequest, error) {
	var pr githubPR
	path, err := repoPath(in.Repository)
	if err != nil {
		return PullRequest{}, err
	}
	payload := struct {
		Title string `json:"title"`
		Head  string `json:"head"`
		Base  string `json:"base"`
		Body  string `json:"body"`
	}{in.Title, in.Head, in.Base, in.Body}
	if err := c.request(ctx, http.MethodPost, path+"/pulls", payload, &pr); err != nil {
		return PullRequest{}, err
	}
	return toPR(pr), nil
}
func (c Client) UpdatePR(ctx context.Context, number int, in PullRequestInput) (PullRequest, error) {
	var pr githubPR
	path, err := repoPath(in.Repository)
	if err != nil {
		return PullRequest{}, err
	}
	payload := struct {
		Title string `json:"title"`
		Head  string `json:"head"`
		Base  string `json:"base"`
		Body  string `json:"body"`
	}{in.Title, in.Head, in.Base, in.Body}
	if err := c.request(ctx, http.MethodPatch, fmt.Sprintf("%s/pulls/%d", path, number), payload, &pr); err != nil {
		return PullRequest{}, err
	}
	return toPR(pr), nil
}
func repoPath(repository string) (string, error) {
	parts := strings.Split(repository, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", errors.New("invalid GitHub repository")
	}
	return "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]), nil
}
func toPR(pr githubPR) PullRequest {
	return PullRequest{Number: pr.Number, URL: pr.HTMLURL, Head: pr.Head.Ref, Base: pr.Base.Ref, Body: pr.Body}
}
