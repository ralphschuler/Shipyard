package automation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDiscoverDevContainerDockerfileAndHash(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".devcontainer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".devcontainer", "Dockerfile"), []byte("FROM golang:latest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".devcontainer", "devcontainer.json"), []byte(`{"dockerFile":"Dockerfile","features":{"ghcr.io/devcontainers/features/go:1":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	config, found, err := discoverDevContainer(root)
	if err != nil || !found {
		t.Fatalf("discover: found=%v err=%v", found, err)
	}
	if config.Dockerfile == "" || config.Hash == "" || !config.HasFeatures {
		t.Fatalf("incomplete config: %+v", config)
	}
	if config.Hash == strings.Repeat("0", 64) {
		t.Fatal("definition hash was not calculated")
	}
	if err := reviewDevContainerPolicy(config, devContainerApproval{}); err != nil {
		t.Fatalf("workspace dockerfile with language features was rejected: %v", err)
	}
}

func TestDiscoverDevContainerComposeArrayAndMissingFile(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".devcontainer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".devcontainer", "compose.yml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".devcontainer", "devcontainer.json"), []byte(`{"dockerComposeFile":["compose.yml"],"service":"app"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	config, found, err := discoverDevContainer(root)
	if err != nil || !found || len(config.ComposeFiles) != 1 {
		t.Fatalf("compose discover: found=%v config=%+v err=%v", found, config, err)
	}
	if err := os.Remove(config.ComposeFiles[0]); err != nil {
		t.Fatal(err)
	}
	if _, found, err := discoverDevContainer(root); err == nil || !found {
		t.Fatal("missing compose file was not rejected")
	}
}

func TestDiscoverDevContainerFallbackAndExecArgs(t *testing.T) {
	root := t.TempDir()
	if config, found, err := discoverDevContainer(root); err != nil || found || config.Hash != "" {
		t.Fatalf("unexpected fallback result: %+v found=%v err=%v", config, found, err)
	}
	config := devContainerConfig{Root: root, WorkspaceFolder: "/workspace/project"}
	command, args := devContainerExecArgs("devcontainer", config, "go", []string{"test", "./..."})
	if command != "devcontainer" || strings.Join(args, " ") != "exec --workspace-folder "+root+" go test ./..." {
		t.Fatalf("unexpected exec args: %s %v", command, args)
	}
	if got := devContainerWorkspaceFolder(config, root); got != "/workspace/project" {
		t.Fatal(got)
	}
	config = devContainerConfig{Root: root, WorkspaceMount: "source=${localWorkspaceFolder},target=/workspace/mounted,type=bind"}
	if got := devContainerWorkspaceFolder(config, root); got != "/workspace/mounted" {
		t.Fatalf("workspace mount target was not resolved: %q", got)
	}
}

func TestReviewDevcontainerRejectsHostHooksAndMounts(t *testing.T) {
	root := writeDevcontainer(t, `{
		"image":"mcr.microsoft.com/devcontainers/base:ubuntu",
		"initializeCommand":"touch /tmp/shipyard-host-hook",
		"privileged":true,
		"mounts":["source=/,target=/host,type=bind"]
	}`)
	config, found, err := discoverDevContainer(root)
	if err != nil || !found {
		t.Fatalf("discover: found=%v err=%v", found, err)
	}
	if config.Hash == "" {
		t.Fatal("definition hash is an audit identifier and must still be calculated")
	}
	runtime := writeFakeDevcontainerRuntime(t)
	err = startDevContainerAfterSandbox(context.Background(), runtime, config, "run-sec04", nil, devContainerApproval{})
	if err == nil {
		t.Fatal("unapproved host hooks, privileged mode and host mounts were accepted")
	}
	if !strings.Contains(err.Error(), "initializeCommand") && !strings.Contains(err.Error(), "privileg") && !strings.Contains(err.Error(), "Host-Pfade") {
		t.Fatalf("unexpected policy error: %v", err)
	}
	if fakeRuntimeInvoked(runtime) {
		t.Fatal("disallowed definition reached the Dev Container runtime")
	}
}

func TestReviewDevcontainerRejectsExtraDockerRightsAndComposePrivileges(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".devcontainer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".devcontainer", "compose.yml"), []byte("services:\n  app:\n    image: alpine\n    privileged: true\n    volumes:\n      - /:/host\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".devcontainer", "devcontainer.json"), []byte(`{"dockerComposeFile":"compose.yml","service":"app","runArgs":["--cap-add=SYS_ADMIN"],"features":{"ghcr.io/devcontainers/features/docker-in-docker:2":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	config, found, err := discoverDevContainer(root)
	if err != nil || !found {
		t.Fatalf("discover: found=%v err=%v", found, err)
	}
	runtime := writeFakeDevcontainerRuntime(t)
	if err := startDevContainer(context.Background(), runtime, config, "run-compose", devContainerApproval{}); err == nil {
		t.Fatal("compose privileges, extra Docker rights and docker-in-docker were accepted")
	}
	if fakeRuntimeInvoked(runtime) {
		t.Fatal("disallowed compose definition reached the runtime")
	}
}

func TestReviewDevcontainerDoesNotInheritServiceEnvironment(t *testing.T) {
	root := writeDevcontainer(t, `{"image":"mcr.microsoft.com/devcontainers/base:ubuntu"}`)
	config, found, err := discoverDevContainer(root)
	if err != nil || !found {
		t.Fatalf("discover: found=%v err=%v", found, err)
	}
	if err := reviewDevContainerPolicy(config, devContainerApproval{}); err != nil {
		t.Fatalf("minimal image definition was rejected: %v", err)
	}
	t.Setenv("DATABASE_URL", "postgres://service-secret")
	t.Setenv("SHIPYARD_SECRET_KEY", "master-key")
	t.Setenv("TASKBOARD_INTERNAL_KEY", "internal-key")
	t.Setenv("TASKBOARD_MCP_TOKEN", "mcp-credential")
	t.Setenv("HTTP_PROXY", "http://user:pass@proxy.internal:8080")
	t.Setenv("HTTPS_PROXY", "http://user:pass@proxy.internal:8080")
	t.Setenv("SHIPYARD_PARENT_ONLY_SECRET", "parent-only")
	runtime := writeFakeDevcontainerRuntime(t)
	if err := startDevContainerAfterSandbox(context.Background(), runtime, config, "run-env", nil, devContainerApproval{}); err != nil {
		t.Fatalf("allowed definition failed to start against fake runtime: %v", err)
	}
	if !fakeRuntimeInvoked(runtime) {
		t.Fatal("allowed definition never reached the fake runtime")
	}
	envBytes, readErr := os.ReadFile(filepath.Join(filepath.Dir(runtime), "env.out"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	env := string(envBytes)
	for _, leaked := range []string{
		"DATABASE_URL=",
		"SHIPYARD_SECRET_KEY=",
		"TASKBOARD_INTERNAL_KEY=",
		"TASKBOARD_MCP_TOKEN=",
		"HTTP_PROXY=",
		"HTTPS_PROXY=",
		"SHIPYARD_PARENT_ONLY_SECRET=",
		"postgres://service-secret",
		"parent-only",
		"mcp-credential",
		"master-key",
	} {
		if strings.Contains(env, leaked) {
			t.Fatalf("runner environment inherited %q:\n%s", leaked, env)
		}
	}
	if !strings.Contains(env, "PATH=") {
		t.Fatalf("runner allowlist omitted PATH:\n%s", env)
	}
}

func TestReviewDevcontainerValidatesSandboxBeforeLifecycle(t *testing.T) {
	root := writeDevcontainer(t, `{"image":"mcr.microsoft.com/devcontainers/base:ubuntu"}`)
	config, found, err := discoverDevContainer(root)
	if err != nil || !found {
		t.Fatalf("discover: found=%v err=%v", found, err)
	}
	runtime := writeFakeDevcontainerRuntime(t)
	err = startDevContainerAfterSandbox(context.Background(), runtime, config, "run-sandbox", errors.New("sandbox invalid"), devContainerApproval{})
	if err == nil || !strings.Contains(err.Error(), "Sandbox-Profil") {
		t.Fatalf("sandbox failure did not block lifecycle: %v", err)
	}
	if fakeRuntimeInvoked(runtime) {
		t.Fatal("repository lifecycle started before sandbox validation")
	}

	workerSrc, readErr := os.ReadFile("worker.go")
	if readErr != nil {
		t.Fatal(readErr)
	}
	src := string(workerSrc)
	sandboxIdx := strings.Index(src, "RunSandboxPolicy(")
	startIdx := strings.Index(src, "startDevContainerAfterSandbox(")
	if sandboxIdx < 0 || startIdx < 0 || sandboxIdx > startIdx {
		t.Fatal("production execute path must validate sandbox policy before Dev Container start")
	}
}

func TestReviewDevcontainerAllowsWorkspaceBind(t *testing.T) {
	root := writeDevcontainer(t, `{
		"image":"mcr.microsoft.com/devcontainers/go:1",
		"workspaceMount":"source=${localWorkspaceFolder},target=/workspace,type=bind",
		"mounts":["source=${localWorkspaceFolder}/.cache,target=/tmp/cache,type=bind"]
	}`)
	config, found, err := discoverDevContainer(root)
	if err != nil || !found {
		t.Fatalf("discover: found=%v err=%v", found, err)
	}
	if err := reviewDevContainerPolicy(config, devContainerApproval{}); err != nil {
		t.Fatalf("workspace-local binds were rejected: %v", err)
	}
	runtime := writeFakeDevcontainerRuntime(t)
	if err := startDevContainerAfterSandbox(context.Background(), runtime, config, "run-ok", nil, devContainerApproval{}); err != nil {
		t.Fatalf("workspace-local definition failed: %v", err)
	}
	if !fakeRuntimeInvoked(runtime) {
		t.Fatal("allowed workspace-local definition never reached the fake runtime")
	}
}

func TestReviewDevcontainerLoggedHashIsNotAnApprovalGate(t *testing.T) {
	root := writeDevcontainer(t, `{"image":"mcr.microsoft.com/devcontainers/go:1","initializeCommand":"id"}`)
	config, found, err := discoverDevContainer(root)
	if err != nil || !found || config.Hash == "" {
		t.Fatalf("discover: found=%v hash=%q err=%v", found, config.Hash, err)
	}
	approval := devContainerApproval{Hash: config.Hash}
	if err := reviewDevContainerPolicy(config, approval); err == nil {
		t.Fatal("matching hash without explicit host-hook grant was treated as approval")
	}
	runtime := writeFakeDevcontainerRuntime(t)
	if err := startDevContainer(context.Background(), runtime, config, "run-hash", approval); err == nil || fakeRuntimeInvoked(runtime) {
		t.Fatal("unapproved initializeCommand reached host execution")
	}
}

func TestShipyardDevcontainerMeetsPolicy(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	config, found, err := discoverDevContainer(root)
	if err != nil || !found {
		t.Fatalf("discover repo devcontainer: found=%v err=%v", found, err)
	}
	if err := reviewDevContainerPolicy(config, devContainerApproval{}); err != nil {
		t.Fatalf("repo devcontainer was rejected: %v", err)
	}
	if config.definition.Service != "dev" {
		t.Fatalf("service = %q", config.definition.Service)
	}
	if config.WorkspaceFolder != "/workspaces/Shipyard" {
		t.Fatalf("workspace = %q", config.WorkspaceFolder)
	}
	if config.HasFeatures || len(config.FeatureIDs) != 0 {
		t.Fatalf("features = %v", config.FeatureIDs)
	}
	if len(config.ComposeFiles) != 1 {
		t.Fatalf("compose files = %v", config.ComposeFiles)
	}
	if jsonFieldPresent(config.definition.InitializeCommand) || config.definition.Privileged {
		t.Fatal("host hooks or privileged mode are part of the recommended definition")
	}
	if jsonFieldPresent(config.definition.RunArgs) || jsonFieldPresent(config.definition.CapAdd) || jsonFieldPresent(config.definition.SecurityOpt) || jsonFieldPresent(config.definition.Mounts) {
		t.Fatal("extra docker rights or mounts are part of the recommended definition")
	}
	raw, err := os.ReadFile(config.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"initializeCommand", "docker.sock", "\"privileged\""} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("devcontainer.json contains %q", forbidden)
		}
	}
	compose, err := os.ReadFile(config.ComposeFiles[0])
	if err != nil {
		t.Fatal(err)
	}
	text := string(compose)
	for _, forbidden := range []string{"docker.sock", "privileged", "cap_add", "security_opt", "network_mode", "ports:", "source: /", "- /:"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("compose contains %q", forbidden)
		}
	}
	for _, required := range []string{"pg_isready", "postgres:16-alpine", "shipyard_dev_postgres", "dockerfile: Dockerfile"} {
		if !strings.Contains(text, required) {
			t.Fatalf("compose missing %q", required)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".devcontainer", "Dockerfile")); err != nil {
		t.Fatal(err)
	}
}

func writeDevcontainer(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".devcontainer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".devcontainer", "devcontainer.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeFakeDevcontainerRuntime(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "devcontainer")
	script := `#!/bin/sh
set -eu
dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [ "${1:-}" = "version" ]; then
  echo "fake-devcontainer 0.0"
  exit 0
fi
printf '%s\n' "$@" > "$dir/args.out"
env | sort > "$dir/env.out"
echo invoked > "$dir/invoked"
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func fakeRuntimeInvoked(runtime string) bool {
	_, err := os.Stat(filepath.Join(filepath.Dir(runtime), "invoked"))
	return err == nil
}
