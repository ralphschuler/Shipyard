package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentBaseVisibilityTreatsPublicUserPackageAsDone(t *testing.T) {
	env, logPath := visibilityHarness(t, visibilityState{ownerType: "User", visibility: "public"})
	out := runVisibility(t, env, 0)
	log := readLog(t, logPath)
	if !strings.Contains(log, "api /users/acme/packages/container/shipyard-agent-base --jq .visibility") {
		t.Fatalf("did not read the user package:\n%s", log)
	}
	if strings.Contains(log, "--method PUT") {
		t.Fatalf("public package must not be updated:\n%s", log)
	}
	if strings.Contains(log, "/orgs/") {
		t.Fatalf("user owner must not use the org packages route:\n%s", log)
	}
	if !strings.Contains(out, "package ghcr.io/acme/shipyard-agent-base is public") {
		t.Fatalf("output = %q", out)
	}
}

func TestAgentBaseVisibilityPublishesOrgPackage(t *testing.T) {
	env, logPath := visibilityHarness(t, visibilityState{ownerType: "Organization", visibility: "private", putOK: true})
	out := runVisibility(t, env, 0)
	log := readLog(t, logPath)
	if !strings.Contains(log, "api --method PUT -H Accept: application/vnd.github+json /orgs/acme/packages/container/shipyard-agent-base/visibility -f visibility=public") {
		t.Fatalf("org visibility update was not called:\n%s", log)
	}
	if strings.Contains(log, "/users/acme/packages/") {
		t.Fatalf("org owner must not use the user packages route:\n%s", log)
	}
	if !strings.Contains(out, "made ghcr.io/acme/shipyard-agent-base public") {
		t.Fatalf("output = %q", out)
	}
}

func TestAgentBaseVisibilityContinuesWhenUserRouteReturns404(t *testing.T) {
	env, logPath := visibilityHarness(t, visibilityState{ownerType: "User", visibility: "private", putOK: false})
	out := runVisibility(t, env, 0)
	log := readLog(t, logPath)
	if !strings.Contains(log, "/users/acme/packages/container/shipyard-agent-base/visibility") {
		t.Fatalf("user visibility route was not attempted:\n%s", log)
	}
	if !strings.Contains(out, "::warning::") || !strings.Contains(out, "GitHub release will still publish") {
		t.Fatalf("visibility 404 must warn and continue:\n%s", out)
	}
	if strings.Contains(out, "exit 1") {
		t.Fatalf("visibility failure must not be fatal:\n%s", out)
	}
}

func TestAgentBaseVisibilityContinuesWhenPackageIsNotRegistered(t *testing.T) {
	env, logPath := visibilityHarness(t, visibilityState{ownerType: "User"})
	out := runVisibility(t, env, 0)
	log := readLog(t, logPath)
	if strings.Contains(log, "--method PUT") {
		t.Fatalf("visibility must wait until the package is registered:\n%s", log)
	}
	if !strings.Contains(out, "::warning::") || !strings.Contains(out, "was not found") {
		t.Fatalf("missing package must warn and continue:\n%s", out)
	}
}

func TestAgentBaseVisibilityEncodesPackageName(t *testing.T) {
	env, logPath := visibilityHarness(t, visibilityState{ownerType: "Organization", visibility: "public"})
	env = append(env, "AGENT_BASE_PACKAGE=helm/crd-bootstrap")
	runVisibility(t, env, 0)
	log := readLog(t, logPath)
	if !strings.Contains(log, "/orgs/acme/packages/container/helm%2Fcrd-bootstrap") {
		t.Fatalf("package name was not URL-encoded:\n%s", log)
	}
	if strings.Contains(log, "/container/helm/crd-bootstrap") {
		t.Fatalf("raw slash in the package name must not be a path separator:\n%s", log)
	}
}

func TestAgentBaseVisibilityScriptDoesNotFailTheRelease(t *testing.T) {
	body, err := os.ReadFile("publish-agent-base-visibility.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		`/orgs/${owner}/packages/container/${encoded}`,
		`/users/${owner}/packages/container/${encoded}`,
		"-f visibility=public",
		"::warning::",
		"exit 0",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("visibility script missing %q", required)
		}
	}
	if strings.Contains(text, "exit 1") {
		t.Fatal("visibility script must not fail the Release workflow")
	}
}

type visibilityState struct {
	ownerType  string
	visibility string
	putOK      bool
}

func visibilityHarness(t *testing.T, state visibilityState) (env []string, logPath string) {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	mockDir := filepath.Join(root, "mock")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mockDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mockDir, "owner_type"), []byte(state.ownerType+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if state.visibility != "" {
		if err := os.WriteFile(filepath.Join(mockDir, "visibility"), []byte(state.visibility+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if state.putOK {
		if err := os.WriteFile(filepath.Join(mockDir, "put_ok"), []byte("1\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	gh := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "${GH_MOCK_DIR}/commands.log"
args=("$@")
if [[ "${args[0]}" != "api" ]]; then
  exit 2
fi
joined="${args[*]}"
if [[ "$joined" == *"--method PUT"* ]]; then
  if [[ -f "${GH_MOCK_DIR}/put_ok" ]]; then
    printf '{"visibility":"public"}\n'
    exit 0
  fi
  printf 'gh: Not Found (HTTP 404)\n' >&2
  exit 1
fi
if [[ "$joined" == *"--jq .type"* ]]; then
  tr -d '\n' < "${GH_MOCK_DIR}/owner_type"
  printf '\n'
  exit 0
fi
if [[ "$joined" == *"--jq .visibility"* ]]; then
  if [[ -f "${GH_MOCK_DIR}/visibility" ]]; then
    tr -d '\n' < "${GH_MOCK_DIR}/visibility"
    printf '\n'
    exit 0
  fi
  printf 'gh: Not Found (HTTP 404)\n' >&2
  exit 1
fi
exit 2
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(gh), 0755); err != nil {
		t.Fatal(err)
	}
	env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GH_MOCK_DIR="+mockDir,
		"GITHUB_REPOSITORY_OWNER=acme",
		"GH_TOKEN=test-token",
		"VISIBILITY_ATTEMPTS=2",
		"VISIBILITY_SLEEP_SCALE=0",
	)
	return env, filepath.Join(mockDir, "commands.log")
}

func runVisibility(t *testing.T, env []string, wantExit int) string {
	t.Helper()
	script, err := filepath.Abs("publish-agent-base-visibility.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			t.Fatalf("run visibility: %v\n%s", err, out)
		}
	}
	if code != wantExit {
		t.Fatalf("exit = %d, want %d\n%s", code, wantExit, out)
	}
	return string(out)
}
