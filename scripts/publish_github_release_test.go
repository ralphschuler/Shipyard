package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const builtCommit = "0123456789abcdef0123456789abcdef01234567"
const otherCommit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestPublishGitHubReleaseBindsTagToBuiltCommit(t *testing.T) {
	env, logPath := publishHarness(t, mockState{})
	runPublish(t, env, 0, "")
	log := readLog(t, logPath)
	if !strings.Contains(log, "api --method POST repos/acme/shipyard/git/refs -f ref=refs/tags/v0.1.42 -f sha="+builtCommit) {
		t.Fatalf("tag was not created at the built commit:\n%s", log)
	}
	if !strings.Contains(log, "release create v0.1.42") {
		t.Fatalf("release was not created:\n%s", log)
	}
	if !strings.Contains(log, "--target "+builtCommit) {
		t.Fatalf("release create must target the built SHA, not a branch:\n%s", log)
	}
	if strings.Contains(log, "--target master") {
		t.Fatal("release create must not target the moving master branch")
	}
	if !strings.Contains(log, "--verify-tag") {
		t.Fatalf("release create must verify the pre-bound tag:\n%s", log)
	}
}

func TestPublishGitHubReleaseIgnoresMovedBranchHEAD(t *testing.T) {
	env, logPath := publishHarness(t, mockState{branchHEAD: otherCommit})
	runPublish(t, env, 0, "")
	log := readLog(t, logPath)
	if strings.Contains(log, otherCommit) {
		t.Fatalf("moved master HEAD %s must not be used for the tag or release:\n%s", otherCommit, log)
	}
	if !strings.Contains(log, "--target "+builtCommit) {
		t.Fatalf("publish must stay bound to the built commit:\n%s", log)
	}
}

func TestPublishGitHubReleaseRejectsUnapprovedRef(t *testing.T) {
	env, _ := publishHarness(t, mockState{})
	env = append(env, "GITHUB_REF=refs/heads/feature")
	stderr := runPublish(t, env, 1, "--gate-only")
	if !strings.Contains(stderr, "refs/heads/feature is not the approved release ref refs/heads/master") {
		t.Fatalf("unapproved ref error = %q", stderr)
	}
}

func TestPublishGitHubReleaseRejectsExistingTagAtDifferentSHA(t *testing.T) {
	env, _ := publishHarness(t, mockState{tagSHA: otherCommit})
	stderr := runPublish(t, env, 1, "")
	if !strings.Contains(stderr, "already points at "+otherCommit) || !strings.Contains(stderr, builtCommit) {
		t.Fatalf("mismatched tag error = %q", stderr)
	}
}

func TestPublishGitHubReleaseRetryRefreshesIdenticalBinding(t *testing.T) {
	env, logPath := publishHarness(t, mockState{tagSHA: builtCommit, releaseExists: true})
	runPublish(t, env, 0, "")
	log := readLog(t, logPath)
	if strings.Contains(log, "release create") {
		t.Fatalf("retry must not create a second release:\n%s", log)
	}
	if !strings.Contains(log, "release upload v0.1.42") || !strings.Contains(log, "--clobber") {
		t.Fatalf("retry must refresh assets in place:\n%s", log)
	}
	if strings.Contains(log, "git/refs") {
		t.Fatalf("identical existing tag must not be recreated:\n%s", log)
	}
}

func TestPublishGitHubReleaseGateOnlyAcceptsApprovedRef(t *testing.T) {
	env, logPath := publishHarness(t, mockState{})
	out := runPublish(t, env, 0, "--gate-only")
	if !strings.Contains(out, "release eligibility ok") {
		t.Fatalf("gate output = %q", out)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatal("gate-only must not call gh")
	}
}

func TestReleaseWorkflowPinsArtifactsAndTagToRunSHA(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"RELEASE_COMMIT: ${{ github.sha }}",
		"TASKBOARD_COMMIT_SHA: ${{ env.RELEASE_COMMIT }}",
		"./scripts/build-release-artifact.sh",
		"./scripts/publish-github-release.sh",
		"needs: gate",
		"RELEASE_APPROVED_REF: refs/heads/master",
		"packages: write",
		"deploy/agent-base/Dockerfile",
		"runner: ubuntu-24.04-arm",
		"push-by-digest=true",
		"docker buildx imagetools create",
		"needs: [build, image-manifest]",
		"pattern: shipyard-*",
		"ghcr.io/ralphschuler/shipyard-agent-base:${{ env.RELEASE_VERSION }}",
		"ghcr.io/ralphschuler/shipyard-agent-base:${{ steps.image.outputs.minor }}",
		"ghcr.io/ralphschuler/shipyard-agent-base:latest",
		`minor=${RELEASE_VERSION%.*}`,
		"./scripts/publish-agent-base-visibility.sh",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("release workflow missing %q", required)
		}
	}
	for _, platform := range []string{"linux/amd64", "linux/arm64"} {
		if !strings.Contains(text, "platform: "+platform) {
			t.Fatalf("release workflow missing native agent-base platform %q", platform)
		}
	}
	if strings.Contains(text, "docker/setup-qemu-action") || strings.Contains(text, "platforms: linux/amd64,linux/arm64") {
		t.Fatal("agent base platforms must build in parallel on native runners, not one emulated buildx job")
	}
	if strings.Contains(text, "./scripts/build-taskboard.sh") {
		t.Fatal("release workflow must call the shared release-artifact script, not build-taskboard.sh directly")
	}
	if strings.Contains(text, "--target master") || strings.Contains(text, "--target ${{ env.RELEASE_APPROVED_BRANCH }}") {
		t.Fatal("workflow must not create tags at a moving branch HEAD")
	}
	if strings.Contains(text, "-X main.commit=${GITHUB_SHA}") {
		t.Fatal("backend metadata must use the pinned RELEASE_COMMIT")
	}
	if strings.Contains(text, "TASKBOARD_COMMIT_SHA: ${{ github.sha }}") {
		t.Fatal("frontend and backend metadata must use the pinned RELEASE_COMMIT")
	}
	releaseScript, err := os.ReadFile("build-release-artifact.sh")
	if err != nil {
		t.Fatal(err)
	}
	releaseText := string(releaseScript)
	for _, required := range []string{
		`GOOS="${GOOS:-linux}"`,
		`GOARCH="${GOARCH:-amd64}"`,
		`TASKBOARD_LDFLAGS_EXTRA="${TASKBOARD_LDFLAGS_EXTRA:--s -w}"`,
		"npm run build",
		`exec "$script_dir/build-taskboard.sh" "$@"`,
	} {
		if !strings.Contains(releaseText, required) {
			t.Fatalf("build-release-artifact.sh missing %q", required)
		}
	}
	buildScript, err := os.ReadFile("build-taskboard.sh")
	if err != nil {
		t.Fatal(err)
	}
	buildText := string(buildScript)
	for _, required := range []string{
		`build_commit="${TASKBOARD_COMMIT_SHA:-$(git rev-parse HEAD)}"`,
		"-X main.commit=${build_commit}",
		`"$script_dir/scan-go-artifact.sh" "$output"`,
	} {
		if !strings.Contains(buildText, required) {
			t.Fatalf("build-taskboard.sh missing %q", required)
		}
	}
	if !strings.Contains(buildText, `case "$flag" in`) || !strings.Contains(buildText, "-s|-w") {
		t.Fatal("build-taskboard.sh must scan before applying -s/-w strip flags")
	}
	script, err := os.ReadFile("publish-github-release.sh")
	if err != nil {
		t.Fatal(err)
	}
	scriptText := string(script)
	for _, required := range []string{"--target \"$commit\"", "--verify-tag", "refs/tags/${version}"} {
		if !strings.Contains(scriptText, required) {
			t.Fatalf("publish script missing %q", required)
		}
	}
}

type mockState struct {
	tagSHA        string
	releaseExists bool
	branchHEAD    string
}

func publishHarness(t *testing.T, state mockState) (env []string, logPath string) {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	dist := filepath.Join(root, "dist")
	mockDir := filepath.Join(root, "mock")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dist, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(mockDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "shipyard-linux-amd64"), []byte("binary"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "SHA256SUMS"), []byte("deadbeef  shipyard-linux-amd64\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if state.tagSHA != "" {
		if err := os.WriteFile(filepath.Join(mockDir, "tag_sha"), []byte(state.tagSHA+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if state.releaseExists {
		if err := os.WriteFile(filepath.Join(mockDir, "release_exists"), []byte("1\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if state.branchHEAD == "" {
		state.branchHEAD = "ffffffffffffffffffffffffffffffffffffffff"
	}
	if err := os.WriteFile(filepath.Join(mockDir, "branch_head"), []byte(state.branchHEAD+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gh := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "${GH_MOCK_DIR}/commands.log"
args=("$@")
if [[ "${args[0]}" == "api" ]]; then
  if [[ "${args[*]}" == *"--method POST"* && "${args[*]}" == *"git/refs"* ]]; then
    sha=""
    for arg in "${args[@]}"; do
      case "$arg" in
        sha=*) sha="${arg#sha=}" ;;
      esac
    done
    printf '%s\n' "$sha" > "${GH_MOCK_DIR}/tag_sha"
    printf '{"ref":"created"}\n'
    exit 0
  fi
  if [[ "${args[*]}" == *"/commits/"* ]]; then
    if [[ -f "${GH_MOCK_DIR}/tag_sha" ]]; then
      tr -d '\n' < "${GH_MOCK_DIR}/tag_sha"
      printf '\n'
      exit 0
    fi
    exit 1
  fi
  exit 1
fi
if [[ "${args[0]}" == "release" && "${args[1]}" == "view" ]]; then
  if [[ -f "${GH_MOCK_DIR}/release_exists" ]]; then
    exit 0
  fi
  exit 1
fi
if [[ "${args[0]}" == "release" && "${args[1]}" == "create" ]]; then
  printf '%s\n' "$*" > "${GH_MOCK_DIR}/create.log"
  exit 0
fi
if [[ "${args[0]}" == "release" && "${args[1]}" == "upload" ]]; then
  printf '%s\n' "$*" > "${GH_MOCK_DIR}/upload.log"
  exit 0
fi
exit 2
`
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(gh), 0755); err != nil {
		t.Fatal(err)
	}
	env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GH_MOCK_DIR="+mockDir,
		"RELEASE_VERSION=v0.1.42",
		"RELEASE_COMMIT="+builtCommit,
		"GITHUB_REF=refs/heads/master",
		"GITHUB_REPOSITORY=acme/shipyard",
		"RELEASE_DIST_DIR="+dist,
		"GH_TOKEN=test-token",
	)
	return env, filepath.Join(mockDir, "commands.log")
}

func runPublish(t *testing.T, env []string, wantExit int, arg string) string {
	t.Helper()
	script, err := filepath.Abs("publish-github-release.sh")
	if err != nil {
		t.Fatal(err)
	}
	args := []string{script}
	if arg != "" {
		args = append(args, arg)
	}
	cmd := exec.Command("bash", args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			t.Fatalf("run publish: %v\n%s", err, out)
		}
	}
	if code != wantExit {
		t.Fatalf("exit = %d, want %d\n%s", code, wantExit, out)
	}
	return string(out)
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
