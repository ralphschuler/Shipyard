package sandbox

import "testing"

func TestProfilesAreClosedAndSafe(t *testing.T) {
	for _, name := range []string{"strict", "development", "qa-readonly", "release-bridge"} {
		p, err := Get(name)
		if err != nil || !p.Active || len(p.Mounts) != 1 || p.Mounts[0] != "worktree" {
			t.Fatalf("profile %q is not a safe active allowlist entry: %#v %v", name, p, err)
		}
	}
	for _, name := range []string{"", "custom", "danger-full-access", "--bind /etc /etc"} {
		if err := Validate(name); err == nil {
			t.Fatalf("unknown profile %q was accepted", name)
		}
	}
}

func TestMountAndEffectivePolicyValidation(t *testing.T) {
	if err := ValidateMounts([]string{"/etc"}); err == nil {
		t.Fatal("host mount accepted")
	}
	if _, err := Effective("strict", "/"); err == nil {
		t.Fatal("root worktree accepted")
	}
	if p, err := Effective("qa-readonly", "/workspace/run"); err != nil || p.WriteMode != "readonly" {
		t.Fatalf("readonly policy invalid: %#v %v", p, err)
	}
	if _, err := Effective("release-bridge", "/workspace/run"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateProfile(Profile{Name: "qa-network", Mounts: []string{"worktree"}, NetworkMode: "qa-network", WriteMode: "readonly", Active: true}); err != nil {
		t.Fatalf("explicit QA network policy rejected: %v", err)
	}
}

func TestBuiltInProfilesCannotLoseTheirSecurityInvariants(t *testing.T) {
	cases := []Profile{
		{Name: "strict", Mounts: []string{"worktree"}, NetworkMode: "qa-network", WriteMode: "worktree", Active: true},
		{Name: "development", Mounts: []string{"worktree"}, NetworkMode: "none", WriteMode: "readonly", Active: true},
		{Name: "qa-readonly", Mounts: []string{"worktree"}, NetworkMode: "none", WriteMode: "worktree", Active: true},
		{Name: "release-bridge", Mounts: []string{"worktree"}, NetworkMode: "bridge-only", WriteMode: "readonly", Active: false},
	}
	for _, profile := range cases {
		if err := ValidateProfile(profile); err == nil {
			t.Fatalf("modified built-in profile %q was accepted", profile.Name)
		}
	}
}
