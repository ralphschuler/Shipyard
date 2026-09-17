package release

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

type fakeGitHub struct {
	prs     []PullRequest
	created []PullRequestInput
	updated []PullRequestInput
	findErr error
	order   *[]string
	mu      sync.Mutex
}

func (f *fakeGitHub) FindPR(context.Context, string, string, string, string, string) (*PullRequest, error) {
	if f.order != nil {
		*f.order = append(*f.order, "find")
	}
	if f.findErr != nil {
		return nil, f.findErr
	}
	if len(f.prs) == 0 {
		return nil, nil
	}
	pr := f.prs[0]
	return &pr, nil
}
func (f *fakeGitHub) CreatePR(_ context.Context, in PullRequestInput) (PullRequest, error) {
	if f.order != nil {
		*f.order = append(*f.order, "create")
	}
	f.created = append(f.created, in)
	return PullRequest{Number: 7, URL: "https://github.com/acme/app/pull/7", Head: in.Head, Base: in.Base, Body: in.Body}, nil
}
func (f *fakeGitHub) UpdatePR(_ context.Context, number int, in PullRequestInput) (PullRequest, error) {
	if f.order != nil {
		*f.order = append(*f.order, "update")
	}
	if number != 7 {
		return PullRequest{}, errors.New("unexpected PR")
	}
	f.updated = append(f.updated, in)
	return PullRequest{Number: number, URL: "https://github.com/acme/app/pull/7", Head: in.Head, Base: in.Base, Body: in.Body}, nil
}

type fakePusher struct{ calls []PushInput }

func (f *fakePusher) Push(_ context.Context, in PushInput) error {
	f.calls = append(f.calls, in)
	return nil
}

func validRequest() Request {
	return Request{
		TaskID: "541eed12-d2f4-42b6-ba8f-17d363b09d22", ProjectID: "0c8bedae-53b6-4517-895a-3e16816fdb69",
		RepositoryURL: "https://github.com/acme/app.git", SourcePath: "/managed/acme-app", SourceBranch: "task/541eed12-d2f4-42b6-ba8f-17d363b09d22", TargetBranch: "master",
		CommitSHA: "0123456789012345678901234567890123456789", DiffSummary: "change", Tests: "go test ./...", DoneApproved: true,
	}
}

func TestPublishCreatesOnePRForApprovedDoneTask(t *testing.T) {
	gh := &fakeGitHub{}
	pusher := &fakePusher{}
	result, err := Publish(context.Background(), validRequest(), pusher, gh)
	if err != nil {
		t.Fatal(err)
	}
	if len(pusher.calls) != 1 || pusher.calls[0].Force {
		t.Fatalf("push = %#v", pusher.calls)
	}
	if len(gh.created) != 1 || len(gh.updated) != 0 {
		t.Fatalf("created=%d updated=%d", len(gh.created), len(gh.updated))
	}
	if result.PR.URL == "" || result.Marker == "" {
		t.Fatalf("result = %#v", result)
	}
	for _, want := range []string{validRequest().TaskID, validRequest().CommitSHA, "master", "go test ./..."} {
		if !contains(gh.created[0].Body, want) {
			t.Fatalf("PR body lacks %q: %s", want, gh.created[0].Body)
		}
	}
}

func TestPublishUpdatesMatchingPROnRetry(t *testing.T) {
	request := validRequest()
	gh := &fakeGitHub{prs: []PullRequest{{Number: 7, URL: "https://github.com/acme/app/pull/7", Head: request.SourceBranch, Base: request.TargetBranch}}}
	pusher := &fakePusher{}
	result, err := Publish(context.Background(), request, pusher, gh)
	if err != nil {
		t.Fatal(err)
	}
	if len(gh.created) != 0 || len(gh.updated) != 1 {
		t.Fatalf("created=%d updated=%d", len(gh.created), len(gh.updated))
	}
	if result.PR.Number != 7 {
		t.Fatalf("PR = %#v", result.PR)
	}
}

func TestPublishBlocksMissingApprovalOrIdentity(t *testing.T) {
	for name, mutate := range map[string]func(*Request){
		"approval":   func(r *Request) { r.DoneApproved = false },
		"repository": func(r *Request) { r.RepositoryURL = "" },
		"commit":     func(r *Request) { r.CommitSHA = "" },
		"target":     func(r *Request) { r.TargetBranch = "" },
	} {
		t.Run(name, func(t *testing.T) {
			r := validRequest()
			mutate(&r)
			if _, err := Publish(context.Background(), r, &fakePusher{}, &fakeGitHub{}); err == nil {
				t.Fatal("expected fail-closed validation")
			}
		})
	}
}

func TestPublishLooksUpBeforePushAndDoesNotPushWhenLookupFails(t *testing.T) {
	r := validRequest()
	order := []string{}
	gh := &fakeGitHub{findErr: errors.New("lookup secret"), order: &order}
	pusher := &fakePusher{}
	if _, err := Publish(context.Background(), r, pusher, gh); err == nil {
		t.Fatal("expected lookup failure")
	}
	if len(pusher.calls) != 0 {
		t.Fatalf("push calls = %#v", pusher.calls)
	}
	if len(order) != 1 || order[0] != "find" {
		t.Fatalf("order = %v", order)
	}
}

func TestPublishRedactsConfiguredSecretsFromPRAndErrors(t *testing.T) {
	r := validRequest()
	r.SecretValues = []string{"top-secret"}
	r.DiffSummary = "changed top-secret value"
	gh := &fakeGitHub{findErr: errors.New("adapter top-secret failure")}
	_, err := Publish(context.Background(), r, &fakePusher{}, gh)
	if err == nil || contains(err.Error(), "top-secret") {
		t.Fatalf("error leaked secret: %v", err)
	}
	gh.findErr = nil
	result, err := Publish(context.Background(), r, &fakePusher{}, gh)
	if err != nil {
		t.Fatal(err)
	}
	if contains(gh.created[0].Body, "top-secret") || !contains(gh.created[0].Body, "[REDACTED]") {
		t.Fatalf("body = %q", gh.created[0].Body)
	}
	_ = result
}

func TestGitPusherRejectsCommitThatIsNotHead(t *testing.T) {
	dir := t.TempDir()
	runGitTest(t, dir, "init")
	runGitTest(t, dir, "config", "user.email", "test@example.com")
	runGitTest(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, dir, "add", "file")
	runGitTest(t, dir, "commit", "-m", "one")
	old := gitOutputTest(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, dir, "commit", "-am", "two")
	if err := (GitPusher{}).Push(context.Background(), PushInput{RepositoryPath: dir, Remote: "origin", Branch: "task/x", CommitSHA: old}); err == nil {
		t.Fatal("expected stale accepted commit to be rejected")
	}
}

func TestGitHubClientRejectsNonCanonicalAPIHost(t *testing.T) {
	client := Client{BaseURL: "https://evil.example/api", Token: "token"}
	if _, err := client.endpoint("/repos/acme/app"); err == nil {
		t.Fatal("expected non-GitHub API host to be rejected")
	}
}

func runGitTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	if output, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v (%s)", args, err, output)
	}
}

func gitOutputTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	output, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(output[:len(output)-1])
}

func contains(value, want string) bool {
	return len(value) >= len(want) && stringIndex(value, want) >= 0
}
func stringIndex(value, want string) int {
	for i := 0; i+len(want) <= len(value); i++ {
		if value[i:i+len(want)] == want {
			return i
		}
	}
	return -1
}
