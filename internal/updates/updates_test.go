package updates

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestClientDownloadsAndVerifiesReleaseArtifact(t *testing.T) {
	body := "shipyard release binary"
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	client := Client{HTTP: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://downloads.example/shipyard" {
			t.Fatalf("unexpected artifact URL: %s", req.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
	got, err := client.DownloadAndVerify(context.Background(), "https://downloads.example/shipyard", digest)
	if err != nil || string(got) != body {
		t.Fatalf("artifact = %q, err = %v", got, err)
	}
	if _, err := client.DownloadAndVerify(context.Background(), "https://downloads.example/shipyard", strings.Repeat("0", 64)); err == nil {
		t.Fatal("expected checksum mismatch")
	}
}

func TestValidateArtifactURLRequiresConfiguredGitHubRepository(t *testing.T) {
	valid := "https://github.com/ralphschuler/Shipyard/releases/download/v1.3.0/shipyard-linux-arm64"
	if err := ValidateArtifactURL(valid, "ralphschuler/Shipyard"); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{
		"https://evil.example/shipyard",
		"https://github.com/other/project/releases/download/v1.3.0/shipyard",
		"http://github.com/ralphschuler/Shipyard/releases/download/v1.3.0/shipyard",
	} {
		if ValidateArtifactURL(candidate, "ralphschuler/Shipyard") == nil {
			t.Fatalf("expected artifact URL rejection: %s", candidate)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCompareOnlyReportsNewerSemanticRelease(t *testing.T) {
	for _, tc := range []struct{ current, release, want string }{
		{"1.2.0", "1.3.0", "update_available"},
		{"1.3.0", "1.3.0", "up_to_date"},
		{"1.3.0", "1.2.9", "up_to_date"},
		{"development", "1.3.0", "unverified"},
	} {
		if got := Compare(tc.current, tc.release); got != tc.want {
			t.Errorf("Compare(%q,%q) = %q, want %q", tc.current, tc.release, got, tc.want)
		}
	}
}

func TestValidateReleaseRequiresFullTrustChainMetadata(t *testing.T) {
	r := Release{Version: "v1.3.0", Commit: "0123456789012345678901234567890123456789", URL: "https://github.com/ralphschuler/Shipyard/releases/tag/v1.3.0", Verified: true, Compatible: true, Checksum: "0123456789012345678901234567890123456789012345678901234567890123"}
	if err := ValidateRelease(r, "ralphschuler/Shipyard"); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Release){"short commit": func(x *Release) { x.Commit = "abc123" }, "short checksum": func(x *Release) { x.Checksum = "abc" }, "wrong host": func(x *Release) { x.URL = "https://example.com/release" }, "unverified": func(x *Release) { x.Verified = false }} {
		t.Run(name, func(t *testing.T) {
			c := r
			mutate(&c)
			if ValidateRelease(c, "ralphschuler/Shipyard") == nil {
				t.Fatal("expected validation failure")
			}
		})
	}
}

func TestValidateReleaseForBranchRequiresSignedTagCommitOnApprovedBranch(t *testing.T) {
	r := Release{Version: "v1.3.0", Commit: "0123456789012345678901234567890123456789", URL: "https://github.com/ralphschuler/Shipyard/releases/tag/v1.3.0", Verified: true, Compatible: true, Checksum: "0123456789012345678901234567890123456789012345678901234567890123"}
	if err := ValidateReleaseForBranch(r, "ralphschuler/Shipyard", "master", "master", r.Commit, true); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string]struct {
		branch, approvedBranch, tagCommit string
		signed                            bool
	}{
		"empty branch": {branch: ""},
		"wrong branch": {branch: "main", approvedBranch: "master", tagCommit: r.Commit, signed: true},
		"tag mismatch": {branch: "master", approvedBranch: "master", tagCommit: "abcdefabcdefabcdefabcdefabcdefabcdefabcd", signed: true},
		"unsigned tag": {branch: "master", approvedBranch: "master", tagCommit: r.Commit},
	} {
		t.Run(name, func(t *testing.T) {
			if ValidateReleaseForBranch(r, "ralphschuler/Shipyard", args.branch, args.approvedBranch, args.tagCommit, args.signed) == nil {
				t.Fatal("expected branch/tag trust failure")
			}
		})
	}
}

func TestResolveRejectsTagNotContainedInApprovedBranch(t *testing.T) {
	commit := strings.Repeat("a", 40)
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body string
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			body = fmt.Sprintf(`{"tag_name":"v1.3.0","html_url":"https://github.com/acme/shipyard/releases/tag/v1.3.0","published_at":"2026-09-17T10:00:00Z","target_commitish":"master","assets":[{"name":"shipyard-linux-amd64","browser_download_url":"https://github.com/acme/shipyard/releases/download/v1.3.0/shipyard-linux-amd64","digest":"sha256:%s"}]}`, strings.Repeat("0", 64))
		case strings.HasSuffix(r.URL.Path, "/commits/v1.3.0"):
			body = fmt.Sprintf(`{"sha":"%s","commit":{"verification":{"verified":true}}}`, commit)
		case strings.HasSuffix(r.URL.Path, "/compare/master..."+commit):
			body = `{"status":"diverged","ahead_by":2,"behind_by":1}`
		default:
			return nil, fmt.Errorf("unexpected GitHub path %s", r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	snapshot := Resolve(context.Background(), Current{Version: "1.2.0"}, "acme/shipyard", "master", Client{HTTP: &http.Client{Transport: transport}, GOOS: "linux", GOARCH: "amd64"})
	if snapshot.Installable || snapshot.Status != "unverified" {
		t.Fatalf("snapshot = %#v, want non-installable unverified release", snapshot)
	}
}

func TestResolveRequiresExplicitReleaseAllowlist(t *testing.T) {
	client := Client{HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"tag_name":"v1.3.0"}`)), Header: make(http.Header)}, nil
	})}, GOOS: "linux", GOARCH: "amd64"}
	snapshot := Resolve(context.Background(), Current{Version: "1.2.0"}, "ralphschuler/Shipyard", "master", client)
	if snapshot.Installable || snapshot.Status != "unverified" {
		t.Fatalf("snapshot = %#v, want an unverified snapshot without an allowlist", snapshot)
	}
}

func TestResolveRejectsReleaseOutsideExplicitAllowlist(t *testing.T) {
	client := Client{ApprovedTags: []string{"v1.4.0"}, HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"tag_name":"v1.3.0","target_commitish":"master"}`)), Header: make(http.Header)}, nil
	})}, GOOS: "linux", GOARCH: "amd64"}
	snapshot := Resolve(context.Background(), Current{Version: "1.2.0"}, "ralphschuler/Shipyard", "master", client)
	if snapshot.Installable || snapshot.Status != "unverified" {
		t.Fatalf("snapshot = %#v, want an unverified snapshot for a non-allowlisted tag", snapshot)
	}
}

func TestOrchestratorBacksUpBeforeInstallAndRollsBackAfterFailure(t *testing.T) {
	var calls []string
	var switchedArtifact []byte
	noop := func(name string) func(context.Context, Snapshot) error {
		return func(context.Context, Snapshot) error { calls = append(calls, name); return nil }
	}
	o := Orchestrator{
		Backup: func(context.Context, Snapshot) error { calls = append(calls, "backup"); return nil },
		DownloadAndVerify: func(context.Context, string, string) ([]byte, error) {
			calls = append(calls, "download")
			return []byte("artifact"), nil
		},
		Verify: func(context.Context, Snapshot) error { calls = append(calls, "verify"); return nil },
		Migrate: func(context.Context, Snapshot) error {
			calls = append(calls, "migrate")
			return errors.New("migration failed")
		},
		Switch: func(_ context.Context, _ Snapshot, artifact []byte) error {
			calls = append(calls, "switch")
			switchedArtifact = append([]byte(nil), artifact...)
			return nil
		},
		Restart:  noop("restart"),
		Health:   noop("health"),
		Rollback: func(context.Context, Snapshot) error { calls = append(calls, "rollback"); return nil },
	}
	s := Snapshot{Status: "update_available", Installable: true, Release: Release{Version: "v1.3.0"}}
	err := o.Install(context.Background(), s, func(Progress) {})
	if err == nil || !reflect.DeepEqual(calls, []string{"backup", "download", "verify", "migrate", "rollback"}) {
		t.Fatalf("error = %v, calls = %v", err, calls)
	}
	if len(switchedArtifact) != 0 {
		t.Fatalf("switch must not run after migration failure: artifact = %q", switchedArtifact)
	}
}

func TestOrchestratorPassesVerifiedArtifactToSwitch(t *testing.T) {
	var switched []byte
	noop := func(context.Context, Snapshot) error { return nil }
	o := Orchestrator{
		Backup: noop,
		DownloadAndVerify: func(context.Context, string, string) ([]byte, error) {
			return []byte("verified-artifact"), nil
		},
		Verify:  noop,
		Migrate: noop,
		Switch: func(_ context.Context, _ Snapshot, artifact []byte) error {
			switched = append([]byte(nil), artifact...)
			return nil
		},
		Restart:  noop,
		Health:   noop,
		Rollback: noop,
	}
	if err := o.Install(context.Background(), Snapshot{Status: "update_available", Installable: true}, nil); err != nil {
		t.Fatal(err)
	}
	if string(switched) != "verified-artifact" {
		t.Fatalf("switch received %q, want the downloaded verified artifact", switched)
	}
}

func TestOrchestratorRequiresRecoveryBeforeMutation(t *testing.T) {
	called := false
	o := Orchestrator{Backup: func(context.Context, Snapshot) error { called = true; return nil }}
	err := o.Install(context.Background(), Snapshot{Status: "update_available", Installable: true}, nil)
	if err == nil || called {
		t.Fatalf("error = %v, backup called = %v", err, called)
	}
}

func TestOrchestratorRequiresArtifactVerificationBeforeMutation(t *testing.T) {
	backupCalled := false
	o := Orchestrator{
		Backup:   func(context.Context, Snapshot) error { backupCalled = true; return nil },
		Verify:   func(context.Context, Snapshot) error { return nil },
		Migrate:  func(context.Context, Snapshot) error { return nil },
		Switch:   func(context.Context, Snapshot, []byte) error { return nil },
		Restart:  func(context.Context, Snapshot) error { return nil },
		Health:   func(context.Context, Snapshot) error { return nil },
		Rollback: func(context.Context, Snapshot) error { return nil },
	}
	err := o.Install(context.Background(), Snapshot{Status: "update_available", Installable: true, Release: Release{
		ArtifactURL: "https://github.com/ralphschuler/Shipyard/releases/download/v1.3.0/shipyard-linux-amd64",
		Checksum:    strings.Repeat("a", 64),
	}}, nil)
	if err == nil || backupCalled {
		t.Fatalf("error = %v, backup called = %v; missing artifact verification must fail before mutation", err, backupCalled)
	}
}

func TestOrchestratorBlocksBusyRunsAndDoesNotMutate(t *testing.T) {
	called := false
	o := Orchestrator{Busy: func(context.Context) bool { return true }, Backup: func(context.Context, Snapshot) error { called = true; return nil }}
	err := o.Install(context.Background(), Snapshot{Status: "update_available", Installable: true}, func(Progress) {})
	if err == nil || called {
		t.Fatalf("error = %v, backup called = %v", err, called)
	}
}
