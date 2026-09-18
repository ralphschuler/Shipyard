package updates

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLoadLocalChangelogUsesConfiguredFileAndEnforcesSizeLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "release-notes.md")
	if err := os.WriteFile(path, []byte("# Local notes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	content, gotPath, err := LoadLocalChangelog(path)
	if err != nil || content != "# Local notes\n" || gotPath != path {
		t.Fatalf("LoadLocalChangelog() = %q, %q, %v", content, gotPath, err)
	}
	if err := os.WriteFile(path, make([]byte, maxLocalChangelogSize+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadLocalChangelog(path); err == nil {
		t.Fatal("expected oversized changelog to be rejected")
	}
}

func TestLoadLocalChangelogDiscoversConventionalNames(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CHANGELOG.markdown")
	if err := os.WriteFile(path, []byte("local"), 0600); err != nil {
		t.Fatal(err)
	}
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	content, gotPath, err := LoadLocalChangelog("")
	if err != nil || content != "local" || gotPath != "CHANGELOG.markdown" {
		t.Fatalf("LoadLocalChangelog() = %q, %q, %v", content, gotPath, err)
	}
}

func TestResolveKeepsLocalChangelogWhenGitHubTokenIsMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(path, []byte("# Local release\n"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot := Resolve(context.Background(), Current{Version: "1.2.0"}, "acme/shipyard", "master", Client{LocalChangelogPath: path, ApprovedTags: []string{"v1.3.*"}})
	if snapshot.Release.Changelog != "# Local release\n" || snapshot.Release.ChangelogSource == "" {
		t.Fatalf("snapshot = %#v, want local changelog", snapshot)
	}
}

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

func TestClientAuthenticatesGitHubArtifactDownloadWithoutLeakingToken(t *testing.T) {
	var githubAuthorization, externalAuthorization string
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "github.com":
			githubAuthorization = req.Header.Get("Authorization")
		case "downloads.example":
			externalAuthorization = req.Header.Get("Authorization")
		}
		body := "shipyard release binary"
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte("shipyard release binary")))
	client := Client{Token: "read-only-token", HTTP: &http.Client{Transport: transport}}
	if _, err := client.DownloadAndVerify(context.Background(), "https://github.com/ralphschuler/Shipyard/releases/download/v1.3.0/shipyard-linux-amd64", digest); err != nil {
		t.Fatal(err)
	}
	if githubAuthorization != "Bearer read-only-token" {
		t.Fatalf("GitHub authorization = %q", githubAuthorization)
	}
	if _, err := client.DownloadAndVerify(context.Background(), "https://downloads.example/shipyard", digest); err != nil {
		t.Fatal(err)
	}
	if externalAuthorization != "" {
		t.Fatalf("external authorization leaked = %q", externalAuthorization)
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

func TestTerminateRunningBundleStopsCandidateProcess(t *testing.T) {
	if os.Getenv("TASKBOARD_BUNDLE_HELPER") == "1" {
		select {}
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestTerminateRunningBundleStopsCandidateProcess")
	cmd.Env = append(os.Environ(), "TASKBOARD_BUNDLE_HELPER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := terminateRunningBundle(os.Args[0]); err != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("terminate candidate process: %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("candidate process remained running after rollback termination")
	}
}

func TestTaskboardRunnerStaysAliveForRollbackHandoff(t *testing.T) {
	dir := t.TempDir()
	fakeBinary := filepath.Join(dir, "clean-exit-child.sh")
	if err := os.WriteFile(fakeBinary, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	runner := filepath.Join("..", "..", "deploy", "taskboard-runner.sh")
	cmd := exec.Command("sh", runner)
	cmd.Env = append(os.Environ(), "TASKBOARD_BINARY="+fakeBinary)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
	}()

	time.Sleep(100 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("runner exited after clean child exit: %v", err)
	}
	if err := cmd.Process.Signal(syscall.SIGUSR1); err != nil {
		t.Fatalf("runner rejected supervisor restart handoff: %v", err)
	}
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("runner exited during rollback handoff: %v", err)
	}
}

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
	client := Client{Token: "read-only-token", ApprovedTags: []string{"v1.4.0"}, HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"tag_name":"v1.3.0","target_commitish":"master"}`)), Header: make(http.Header)}, nil
	})}, GOOS: "linux", GOARCH: "amd64"}
	snapshot := Resolve(context.Background(), Current{Version: "1.2.0"}, "ralphschuler/Shipyard", "master", client)
	if snapshot.Installable || snapshot.Status != "unverified" {
		t.Fatalf("snapshot = %#v, want an unverified snapshot for a non-allowlisted tag", snapshot)
	}
}

func TestValidateReleaseAllowlistSupportsExactTagsAndPatchPrefixes(t *testing.T) {
	for _, policy := range [][]string{{"v0.1.5"}, {"v0.1.*"}, {"  v0.1.*  ", "v1.2.3"}} {
		if err := ValidateReleaseAllowlist(policy); err != nil {
			t.Fatalf("policy %v rejected: %v", policy, err)
		}
	}
	if !tagApproved("v0.1.7", []string{"v0.1.*"}) {
		t.Fatal("patch prefix should approve a future patch release")
	}
	if tagApproved("v0.2.0", []string{"v0.1.*"}) {
		t.Fatal("patch prefix must not approve another minor release")
	}
	if err := ValidateReleaseAllowlist(nil); !errors.Is(err, ErrReleaseAllowlistMissing) {
		t.Fatalf("missing policy error = %v", err)
	}
	if err := ValidateReleaseAllowlist([]string{"latest"}); !errors.Is(err, ErrReleaseAllowlistMalformed) {
		t.Fatalf("malformed policy error = %v", err)
	}
}

func TestClientUsesConfiguredGitHubToken(t *testing.T) {
	var authorization string
	client := Client{Token: "github-token", HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		authorization = r.Header.Get("Authorization")
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"tag_name":"v0.1.5"}`)), Header: make(http.Header)}, nil
	})}}
	if _, err := client.latest(context.Background(), "ralphschuler/Shipyard"); err != nil {
		t.Fatal(err)
	}
	if authorization != "Bearer github-token" {
		t.Fatalf("authorization = %q", authorization)
	}
}

func TestResolveReportsPublicAPIRateLimitWithoutProviderBody(t *testing.T) {
	client := Client{Token: "read-only-token", ApprovedTags: []string{"v0.1.*"}, HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusForbidden, Status: "403 Forbidden", Body: io.NopCloser(strings.NewReader("provider secret response")), Header: make(http.Header)}, nil
	})}, GOOS: "linux", GOARCH: "amd64"}
	snapshot := Resolve(context.Background(), Current{Version: "v0.1.3"}, "ralphschuler/Shipyard", "master", client)
	if snapshot.Status != "unavailable" || !strings.Contains(snapshot.Reason, "Rate-Limit") {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if strings.Contains(snapshot.Reason, "provider secret") {
		t.Fatalf("provider response leaked: %q", snapshot.Reason)
	}
}

func TestResolveFailsClosedWhenGitHubTokenIsMissing(t *testing.T) {
	requests := 0
	client := Client{ApprovedTags: []string{"v0.1.*"}, HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"tag_name":"v0.1.5"}`)), Header: make(http.Header)}, nil
	})}}
	snapshot := Resolve(context.Background(), Current{Version: "v0.1.3"}, "ralphschuler/Shipyard", "master", client)
	if snapshot.Status != "unavailable" || !strings.Contains(snapshot.Reason, "TASKBOARD_GITHUB_TOKEN") {
		t.Fatalf("snapshot = %#v, want actionable missing-token status", snapshot)
	}
	if requests != 0 {
		t.Fatalf("missing-token configuration should fail before GitHub requests, got %d", requests)
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
		VerifyArtifact: func(context.Context, Snapshot, []byte) error { calls = append(calls, "signature"); return nil },
		Verify:         func(context.Context, Snapshot) error { calls = append(calls, "verify"); return nil },
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
	if err == nil || !reflect.DeepEqual(calls, []string{"backup", "download", "signature", "verify", "migrate", "rollback"}) {
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
		VerifyArtifact: func(context.Context, Snapshot, []byte) error { return nil },
		Verify:         noop,
		Migrate:        noop,
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

func TestOrchestratorDoesNotHealthCheckTheOldProcessAfterVerifiedRestart(t *testing.T) {
	healthCalled := false
	noop := func(context.Context, Snapshot) error { return nil }
	o := Orchestrator{
		Backup: noop, DownloadAndVerify: func(context.Context, string, string) ([]byte, error) { return []byte("artifact"), nil },
		VerifyArtifact: func(context.Context, Snapshot, []byte) error { return nil },
		Verify:         noop, Migrate: noop, Switch: func(context.Context, Snapshot, []byte) error { return nil },
		Restart: noop,
		Health: func(context.Context, Snapshot) error {
			healthCalled = true
			return errors.New("old process must not be accepted")
		},
		RestartVerifiesHealth: true,
		Rollback:              noop,
	}
	if err := o.Install(context.Background(), Snapshot{Status: "update_available", Installable: true}, nil); err != nil {
		t.Fatal(err)
	}
	if healthCalled {
		t.Fatal("generic healthcheck must not validate the old process after a verified restart")
	}
}

func TestOrchestratorRequiresArtifactSignatureVerificationBeforeMutation(t *testing.T) {
	backupCalled := false
	o := Orchestrator{
		Backup: func(context.Context, Snapshot) error { backupCalled = true; return nil },
		DownloadAndVerify: func(context.Context, string, string) ([]byte, error) {
			return []byte("artifact"), nil
		},
		Verify:   func(context.Context, Snapshot) error { return nil },
		Migrate:  func(context.Context, Snapshot) error { return nil },
		Switch:   func(context.Context, Snapshot, []byte) error { return nil },
		Restart:  func(context.Context, Snapshot) error { return nil },
		Health:   func(context.Context, Snapshot) error { return nil },
		Rollback: func(context.Context, Snapshot) error { return nil },
	}
	err := o.Install(context.Background(), Snapshot{Status: "update_available", Installable: true}, nil)
	if err == nil || backupCalled {
		t.Fatalf("error = %v, backup called = %v; missing artifact signature verification must fail before mutation", err, backupCalled)
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

func TestProductionAdapterSwitchAndRollbackKeepOneBinaryBundle(t *testing.T) {
	dir := t.TempDir()
	p := &productionAdapter{binary: filepath.Join(dir, "taskboard"), previous: filepath.Join(dir, "taskboard.previous-update")}
	oldBundle := []byte("old-backend-with-old-embedded-assets")
	newBundle := []byte("new-backend-with-new-embedded-assets")
	if err := os.WriteFile(p.binary, oldBundle, 0755); err != nil {
		t.Fatal(err)
	}
	if err := p.switchBinary(context.Background(), Snapshot{}, newBundle); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(p.binary)
	if err != nil || string(got) != string(newBundle) {
		t.Fatalf("installed bundle = %q, err = %v", got, err)
	}
	if err := p.rollback(context.Background(), Snapshot{}); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(p.binary)
	if err != nil || string(got) != string(oldBundle) {
		t.Fatalf("rolled back bundle = %q, err = %v", got, err)
	}
}

func TestProductionAdapterRestartValidatesInstalledBundle(t *testing.T) {
	p := &productionAdapter{binary: filepath.Join(t.TempDir(), "missing-taskboard")}
	if err := p.restart(context.Background(), Snapshot{}); err == nil {
		t.Fatal("restart must reject a candidate that cannot validate its embedded bundle")
	}
}

func TestProductionAdapterRestartWaitsForMonitorResult(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "taskboard")
	started := filepath.Join(dir, "monitor-started")
	release := filepath.Join(dir, "release-monitor")
	marker := filepath.Join(dir, "monitor-finished")
	script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in\n--validate-embedded-app) exit 0 ;;\n--monitor-restart) : > %q; while [ ! -f %q ]; do sleep 0.01; done; : > %q; exit 0 ;;\n*) exit 2 ;;\nesac\n", started, release, marker)
	if err := os.WriteFile(binary, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	p := &productionAdapter{binary: binary, previous: filepath.Join(dir, "previous")}
	go func() {
		if !waitForRestartHelper(t, started) {
			return
		}
		_ = os.WriteFile(release, []byte("release"), 0600)
	}()
	if err := p.restart(context.Background(), Snapshot{Release: Release{Version: "v2", Commit: "candidate"}}); err != nil {
		t.Fatalf("restart() error = %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("monitor completion was not observed: %v", err)
	}
}

func TestRequestSupervisorRestartSignalsConfiguredSupervisor(t *testing.T) {
	if os.Getenv("TASKBOARD_SUPERVISOR_SIGNAL_HELPER") == "1" {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGUSR1)
		if marker := os.Getenv("TASKBOARD_SUPERVISOR_SIGNAL_READY"); marker != "" {
			_ = os.WriteFile(marker, []byte("ready"), 0600)
		}
		<-signals
		os.Exit(0)
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestRequestSupervisorRestartSignalsConfiguredSupervisor")
	ready := filepath.Join(t.TempDir(), "ready")
	cmd.Env = append(os.Environ(), "TASKBOARD_SUPERVISOR_SIGNAL_HELPER=1", "TASKBOARD_SUPERVISOR_SIGNAL_READY="+ready)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	if !waitForRestartHelper(t, ready) {
		t.Fatal("supervisor helper did not become ready")
	}

	if err := requestSupervisorRestartPID(context.Background(), cmd.Process.Pid); err != nil {
		t.Fatalf("requestSupervisorRestart() error = %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("supervisor helper did not receive restart signal: %v", err)
	}
}

func TestRestartMonitorWaitsForNewHealthAndRollsBackOnFailure(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "taskboard")
	previous := filepath.Join(dir, "taskboard.previous-update")
	if err := os.WriteFile(binary, []byte("new-bundle"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(previous, []byte("old-bundle"), 0755); err != nil {
		t.Fatal(err)
	}

	phase := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status := http.StatusServiceUnavailable
		body := ""
		switch phase {
		case 0:
			phase = 1
		case 1:
			phase = 2
			status = http.StatusOK
		case 2:
			status = http.StatusOK
			if strings.HasSuffix(r.URL.Path, "build-info.json") {
				body = `{"version":"v2.0.0","commit":"newcommit"}`
			}
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := monitorRestart(ctx, restartMonitorConfig{
		healthURL:    "http://restart.test/healthz",
		buildInfoURL: "http://restart.test/app/build-info.json",
		binary:       binary,
		previous:     previous,
		version:      "v2.0.0",
		commit:       "newcommit",
		poll:         1 * time.Millisecond,
		client:       client,
	})
	if err != nil {
		t.Fatalf("monitor succeeded unexpectedly: %v", err)
	}

	phase = 3
	terminated := false
	restarted := false
	err = monitorRestart(ctx, restartMonitorConfig{
		healthURL:    "http://restart.test/healthz",
		buildInfoURL: "http://restart.test/app/build-info.json",
		binary:       binary,
		previous:     previous,
		version:      "v2.0.0",
		commit:       "newcommit",
		poll:         1 * time.Millisecond,
		wait:         5 * time.Millisecond,
		client:       client,
		terminate: func(context.Context) error {
			terminated = true
			return nil
		},
		restart: func(context.Context) error {
			restarted = true
			return nil
		},
	})
	if err == nil {
		t.Fatal("monitor must fail when the restarted service never becomes healthy")
	}
	if !terminated {
		t.Fatal("monitor must terminate the failed restarted bundle before restoring it")
	}
	if !restarted {
		t.Fatal("monitor must restart the restored bundle after rollback")
	}
	got, readErr := os.ReadFile(binary)
	if readErr != nil || string(got) != "old-bundle" {
		t.Fatalf("rollback bundle = %q, err = %v", got, readErr)
	}
}

func TestRestartMonitorEndToEndRestartsPreviousBundleAndVerifiesIt(t *testing.T) {
	if os.Getenv("TASKBOARD_RESTART_HELPER") == "1" {
		serveRestartHelper(t)
		return
	}

	dir := t.TempDir()
	binary := filepath.Join(dir, "candidate-bundle")
	previous := filepath.Join(dir, "previous-bundle")
	if err := os.WriteFile(binary, []byte("candidate"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(previous, []byte("previous"), 0755); err != nil {
		t.Fatal(err)
	}

	phase := 0
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status := http.StatusOK
		body := `{"version":"v1.0.0","commit":"old-commit"}`
		switch phase {
		case 0:
			phase = 1
		case 1:
			if r.URL.Path == "/healthz" {
				status = http.StatusOK
			} else {
				body = `{"version":"v2.0.0","commit":"candidate"}`
			}
		}
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	started := make(chan struct{})
	client := &http.Client{Transport: transport}

	terminated := false
	restarted := false
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := monitorRestart(ctx, restartMonitorConfig{
		healthURL:    "http://restart.test/healthz",
		buildInfoURL: "http://restart.test/app/build-info.json",
		binary:       binary,
		previous:     previous,
		version:      "v1.0.0",
		commit:       "old-commit",
		poll:         time.Millisecond,
		wait:         100 * time.Millisecond,
		client:       client,
		terminate: func(context.Context) error {
			terminated = true
			return nil
		},
		restart: func(context.Context) error {
			restarted = true
			phase = 2
			cmd := exec.Command(os.Args[0], "-test.run=TestRestartMonitorEndToEndRestartsPreviousBundleAndVerifiesIt")
			cmd.Env = append(os.Environ(), "TASKBOARD_RESTART_HELPER=1", "TASKBOARD_RESTART_MARKER="+filepath.Join(dir, "started"))
			if err := cmd.Start(); err != nil {
				return err
			}
			close(started)
			return nil
		},
	})
	if err == nil {
		t.Fatal("monitor must fail the candidate update")
	}
	if !terminated || !restarted {
		t.Fatalf("rollback state: terminated=%v restarted=%v", terminated, restarted)
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("restored bundle was not started")
	}
	if got, readErr := os.ReadFile(binary); readErr != nil || string(got) != "previous" {
		t.Fatalf("restored binary = %q, err = %v", got, readErr)
	}
	if !waitForRestartHelper(t, filepath.Join(dir, "started")) {
		t.Fatal("restored bundle did not become reachable")
	}
	if !endpointHealthy(context.Background(), client, "http://restart.test/healthz") || !buildInfoMatches(context.Background(), client, "http://restart.test/app/build-info.json", "v1.0.0", "old-commit") {
		t.Fatal("restored bundle health/build metadata verification failed")
	}
}

func TestRestartMonitorVerifiesEmbeddedAppAfterSupervisorRollback(t *testing.T) {
	state := 0 // old process, stopping, candidate process, supervisor-restored old process
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			if state == 0 {
				state = 1
				w.WriteHeader(http.StatusOK)
				return
			}
			if state == 1 {
				state = 2
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/app/build-info.json" {
			if state == 2 {
				_, _ = io.WriteString(w, `{"version":"v2.0.0","commit":"candidate"}`)
				return
			}
			_, _ = io.WriteString(w, `{"version":"v1.0.0","commit":"old-commit"}`)
			return
		}
		if r.URL.Path == "/app/" {
			if state == 2 {
				_, _ = io.WriteString(w, `<div id="root"><script src="/app/assets/app-candidate-12345678.js"></script></div>`)
				return
			}
			_, _ = io.WriteString(w, `<div id="root"><script src="/app/assets/app-old-12345678.js"></script></div>`)
			return
		}
		if r.URL.Path == "/app/assets/app-old-12345678.js" && state != 2 {
			_, _ = io.WriteString(w, "old asset")
			return
		}
		http.NotFound(w, r)
	})
	socketPath := filepath.Join(t.TempDir(), "taskboard.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Skipf("real HTTP supervisor test requires socket binding: %v", err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		return net.Dial("unix", socketPath)
	}}}
	baseURL := "http://taskboard.test"

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	err = monitorRestart(ctx, restartMonitorConfig{
		healthURL:       baseURL + "/healthz",
		buildInfoURL:    baseURL + "/app/build-info.json",
		appURL:          baseURL + "/app/",
		version:         "v2.0.0",
		commit:          "candidate",
		previousVersion: "v1.0.0",
		previousCommit:  "old-commit",
		poll:            time.Millisecond,
		wait:            100 * time.Millisecond,
		client:          client,
		terminate:       func(context.Context) error { return nil },
		start:           func(context.Context) error { return nil },
		restart: func(context.Context) error {
			state = 3
			return nil
		},
	})
	if err == nil {
		t.Fatal("candidate with a missing fingerprint asset must roll back")
	}
	if state != 2 {
		t.Fatalf("supervisor did not restore the previous process, state=%d", state)
	}
}

func serveRestartHelper(t *testing.T) {
	marker := os.Getenv("TASKBOARD_RESTART_MARKER")
	if marker != "" {
		if err := os.WriteFile(marker, []byte("started"), 0600); err != nil {
			os.Exit(2)
		}
	}
}

func waitForRestartHelper(t *testing.T, marker string) bool {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return true
		}
		time.Sleep(time.Millisecond)
	}
	return false
}
