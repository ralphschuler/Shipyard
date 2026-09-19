package automation

import (
	"os"
	"path/filepath"
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
