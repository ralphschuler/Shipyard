package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanGoArtifactDoesNotUseGoRun(t *testing.T) {
	body, err := os.ReadFile("scan-go-artifact.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.Contains(trimmed, "go run ") {
			t.Fatal("scan-go-artifact.sh must not invoke go run; it remaps os.Exit(3) to status 1")
		}
	}
	if !strings.Contains(text, "go build") || !strings.Contains(text, "./cmd/govulngate") {
		t.Fatal("scan-go-artifact.sh must build and exec cmd/govulngate")
	}
	if !strings.Contains(text, "exit 3") {
		t.Fatal("scan-go-artifact.sh must preserve classifier exit 3")
	}
}

func TestScanGoArtifactPreservesGovulngateExit3(t *testing.T) {
	root := repoRoot(t)
	work := t.TempDir()
	artifact := filepath.Join(work, "artifact")
	build := exec.Command("go", "build", "-o", artifact, "./cmd/govulngate")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build scan fixture: %v\n%s", err, out)
	}

	stubDir := filepath.Join(work, "bin")
	if err := os.Mkdir(stubDir, 0755); err != nil {
		t.Fatal(err)
	}
	stub := `#!/usr/bin/env bash
set -euo pipefail
if [[ "${1:-}" == "-h" ]]; then
  printf '  -format string\n'
  exit 0
fi
mode=source
for arg in "$@"; do
  if [[ "$arg" == "-mode=binary" ]]; then
    mode=binary
  fi
done
if [[ "$mode" == "binary" ]]; then
  printf '%s\n' '{"finding":{"osv":"GO-2026-TEST","trace":[{"module":"stdlib","package":"net/url","function":"URL.Parse","symbol":"URL.Parse"}]}}'
  exit 3
fi
printf '%s\n' '{"config":{"scanner_name":"govulncheck"}}'
exit 0
`
	if err := os.WriteFile(filepath.Join(stubDir, "govulncheck"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "scripts/scan-go-artifact.sh", artifact)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"TASKBOARD_GOVULNCHECK="+filepath.Join(stubDir, "govulncheck"),
		"TASKBOARD_GOVULNCHECK_TIMEOUT=30",
		"TASKBOARD_GOVULNGATE_BUILD_TIMEOUT=60",
	)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			t.Fatalf("run scan: %v\n%s", err, out)
		}
	}
	if code != 3 {
		t.Fatalf("scan wrapper exit = %d, want 3 so blocking findings are not remapped\n%s", code, out)
	}
	text := string(out)
	if !strings.Contains(text, "call-path or symbol hits") {
		t.Fatalf("wrapper must report blocking findings, got:\n%s", text)
	}
	if strings.Contains(text, "could not classify") {
		t.Fatalf("exit 3 must not be reported as a classifier failure:\n%s", text)
	}
	if strings.Contains(text, "exit status 3") {
		t.Fatalf("go run remapped the classifier exit; wrapper must exec the binary:\n%s", text)
	}
}

func TestScanGoArtifactPreservesStubClassifierExit3(t *testing.T) {
	root := repoRoot(t)
	work := t.TempDir()
	artifact := filepath.Join(work, "artifact")
	build := exec.Command("go", "build", "-o", artifact, "./cmd/govulngate")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build scan fixture: %v\n%s", err, out)
	}

	stubDir := filepath.Join(work, "bin")
	if err := os.Mkdir(stubDir, 0755); err != nil {
		t.Fatal(err)
	}
	govulncheck := `#!/usr/bin/env bash
if [[ "${1:-}" == "-h" ]]; then
  printf '  -format string\n'
  exit 0
fi
exit 0
`
	gate := `#!/usr/bin/env bash
printf 'artifact toolchain: testdata\n' >&2
exit 3
`
	if err := os.WriteFile(filepath.Join(stubDir, "govulncheck"), []byte(govulncheck), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stubDir, "govulngate"), []byte(gate), 0755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "scripts/scan-go-artifact.sh", artifact)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"TASKBOARD_GOVULNCHECK="+filepath.Join(stubDir, "govulncheck"),
		"TASKBOARD_GOVULNGATE="+filepath.Join(stubDir, "govulngate"),
		"TASKBOARD_GOVULNCHECK_TIMEOUT=10",
	)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			t.Fatalf("run scan: %v\n%s", err, out)
		}
	}
	if code != 3 {
		t.Fatalf("wrapper exit = %d, want 3\n%s", code, out)
	}
	if strings.Contains(string(out), "could not classify") {
		t.Fatalf("stub exit 3 was classified as a scanner failure:\n%s", out)
	}
}

func TestQualityArtifactVulnMatchesReleaseAmd64Inputs(t *testing.T) {
	quality, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "quality.yml"))
	if err != nil {
		t.Fatal(err)
	}
	release, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	qualityText := string(quality)
	releaseText := string(release)
	if !strings.Contains(qualityText, "./scripts/build-release-artifact.sh") {
		t.Fatal("quality artifact-vuln must use the shared release-artifact script")
	}
	if !strings.Contains(releaseText, "./scripts/build-release-artifact.sh") {
		t.Fatal("release workflow must use the shared release-artifact script")
	}
	job := qualityJob(qualityText, "artifact-vuln:")
	if !strings.Contains(job, "GOOS: linux") || !strings.Contains(job, "GOARCH: amd64") {
		t.Fatal("quality artifact scan must pin GOOS=linux GOARCH=amd64")
	}
	if !strings.Contains(job, `TASKBOARD_LDFLAGS_EXTRA: "-s -w"`) {
		t.Fatal("quality artifact scan must use release strip ldflags")
	}
	if strings.Contains(job, "./scripts/build-taskboard.sh") {
		t.Fatal("quality artifact-vuln must not call build-taskboard.sh directly")
	}
	if !strings.Contains(job, "actions/setup-node@v4") {
		t.Fatal("quality artifact-vuln must install Node so the embedded frontend is built")
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func qualityJob(workflow, name string) string {
	idx := strings.Index(workflow, name)
	if idx < 0 {
		return ""
	}
	rest := workflow[idx:]
	if next := strings.Index(rest, "\n  production-browser:"); next > 0 {
		return rest[:next]
	}
	return rest
}
