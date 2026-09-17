package updates

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

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

func TestOrchestratorBacksUpBeforeInstallAndRollsBackAfterFailure(t *testing.T) {
	var calls []string
	noop := func(name string) func(context.Context, Snapshot) error {
		return func(context.Context, Snapshot) error { calls = append(calls, name); return nil }
	}
	o := Orchestrator{
		Backup: func(context.Context, Snapshot) error { calls = append(calls, "backup"); return nil },
		Verify: func(context.Context, Snapshot) error { calls = append(calls, "verify"); return nil },
		Migrate: func(context.Context, Snapshot) error {
			calls = append(calls, "migrate")
			return errors.New("migration failed")
		},
		Switch:   noop("switch"),
		Restart:  noop("restart"),
		Health:   noop("health"),
		Rollback: func(context.Context, Snapshot) error { calls = append(calls, "rollback"); return nil },
	}
	s := Snapshot{Status: "update_available", Installable: true, Release: Release{Version: "v1.3.0"}}
	err := o.Install(context.Background(), s, func(Progress) {})
	if err == nil || !reflect.DeepEqual(calls, []string{"backup", "verify", "migrate", "rollback"}) {
		t.Fatalf("error = %v, calls = %v", err, calls)
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

func TestOrchestratorBlocksBusyRunsAndDoesNotMutate(t *testing.T) {
	called := false
	o := Orchestrator{Busy: func(context.Context) bool { return true }, Backup: func(context.Context, Snapshot) error { called = true; return nil }}
	err := o.Install(context.Background(), Snapshot{Status: "update_available", Installable: true}, func(Progress) {})
	if err == nil || called {
		t.Fatalf("error = %v, backup called = %v", err, called)
	}
}
