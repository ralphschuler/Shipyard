package release

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

type fakePublicationLocker struct{ acquired int }

func (f *fakePublicationLocker) AcquirePublication(context.Context, string) (func(), error) {
	f.acquired++
	return func() {}, nil
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
		RunID: "run-541eed12", DiffRef: "run-541eed12:diff", RepositoryURL: "https://github.com/acme/app.git", SourcePath: "/managed/acme-app", ManagedProjectPath: "/managed/acme-app", SourceBranch: "task/541eed12-d2f4-42b6-ba8f-17d363b09d22", TargetBranch: "master",
		CommitSHA: "0123456789012345678901234567890123456789", DiffSummary: "change", Tests: "go test ./...", DoneApproved: true,
		PublicationLocker: &fakePublicationLocker{},
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
		"run":        func(r *Request) { r.RunID = "" },
		"diff":       func(r *Request) { r.DiffRef = "" },
		"checkout":   func(r *Request) { r.SourcePath = "/managed/other" },
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

func TestPublishRequiresDurablePublicationLock(t *testing.T) {
	r := validRequest()
	r.PublicationLocker = nil
	if _, err := Publish(context.Background(), r, &fakePusher{}, &fakeGitHub{}); err == nil || !strings.Contains(err.Error(), "adapters") {
		t.Fatalf("expected fail-closed lock configuration error, got %v", err)
	}
}

func TestGitHubFindPRFollowsPagination(t *testing.T) {
	request := validRequest()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.RawQuery, "page=2") {
			_, _ = w.Write([]byte(`[{"number":7,"html_url":"https://github.com/acme/app/pull/7","body":"<!-- shipyard-release-task:541eed12-d2f4-42b6-ba8f-17d363b09d22 -->","head":{"ref":"task/541eed12-d2f4-42b6-ba8f-17d363b09d22"},"base":{"ref":"master"}}]`))
			return
		}
		w.Header().Set("Link", `<https://api.github.com/repos/acme/app/pulls?state=open&head=acme%3Atask%2F541eed12-d2f4-42b6-ba8f-17d363b09d22&base=master&page=2>; rel="next"`)
		_, _ = w.Write([]byte(`[]`))
	})
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("IPv4 listener unavailable: %v", err)
	}
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	defer server.Close()
	serverURL := "http://" + listener.Addr().String()
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		r.URL.Scheme = "http"
		r.URL.Host = strings.TrimPrefix(serverURL, "http://")
		return http.DefaultTransport.RoundTrip(r)
	})
	client := Client{BaseURL: "https://api.github.com", Token: "test-token", HTTP: &http.Client{Transport: transport}}
	pr, err := client.FindPR(context.Background(), "acme", "app", request.SourceBranch, request.TargetBranch, "<!-- shipyard-release-task:"+request.TaskID+" -->")
	if err != nil || pr == nil || pr.Number != 7 {
		t.Fatalf("FindPR() = %#v, %v", pr, err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

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

func TestPublishDoesNotPushWhenMatchingPRIsInvalid(t *testing.T) {
	r := validRequest()
	gh := &fakeGitHub{prs: []PullRequest{{Number: 7, URL: "https://github.com/other/repo/pull/7", Head: r.SourceBranch, Base: r.TargetBranch}}}
	pusher := &fakePusher{}
	if _, err := Publish(context.Background(), r, pusher, gh); err == nil {
		t.Fatal("expected invalid matching PR to block release")
	}
	if len(pusher.calls) != 0 {
		t.Fatalf("push calls = %#v", pusher.calls)
	}
}

func TestGitHubRepositoryRejectsQueryAndFragment(t *testing.T) {
	for _, raw := range []string{"https://github.com/acme/app?x=1", "https://github.com/acme/app#fragment"} {
		if _, _, err := githubRepository(raw); err == nil {
			t.Fatalf("expected non-canonical repository URL to be rejected: %s", raw)
		}
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
