package release

import (
	"context"
	"errors"
	"testing"
)

type fakeGitHub struct {
	prs     []PullRequest
	created []PullRequestInput
	updated []PullRequestInput
}

func (f *fakeGitHub) FindPR(context.Context, string, string, string, string, string) (*PullRequest, error) {
	if len(f.prs) == 0 {
		return nil, nil
	}
	pr := f.prs[0]
	return &pr, nil
}
func (f *fakeGitHub) CreatePR(_ context.Context, in PullRequestInput) (PullRequest, error) {
	f.created = append(f.created, in)
	return PullRequest{Number: 7, URL: "https://github.com/acme/app/pull/7", Head: in.Head, Base: in.Base, Body: in.Body}, nil
}
func (f *fakeGitHub) UpdatePR(_ context.Context, number int, in PullRequestInput) (PullRequest, error) {
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
