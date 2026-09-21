package container

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerAndPodmanShareTheProviderContract(t *testing.T) {
	docker := NewDocker()
	podman := NewPodman()
	if docker.Name() != "docker" || podman.Name() != "podman" {
		t.Fatalf("names = %s %s", docker.Name(), podman.Name())
	}
	dir := t.TempDir()
	writeFakeRuntime(t, dir, "docker")
	writeFakeRuntime(t, dir, "podman")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DATABASE_URL", "postgres://service-secret")
	t.Setenv("SHIPYARD_SECRET_KEY", "master-key")
	t.Setenv("HTTP_PROXY", "http://user:pass@proxy.internal:8080")
	for _, provider := range []Provider{docker, podman} {
		if err := provider.Available(context.Background()); err != nil {
			t.Fatal(err)
		}
		id, err := provider.Create(context.Background(), Spec{
			Name:    "shipyard-agent-test",
			Image:   "example/agent:1",
			Network: "none",
			User:    "1:1",
			Workdir: "/work",
			Labels:  map[string]string{"shipyard.run": "run-1"},
			Mounts: []Mount{{
				Source:   filepath.Join(dir, "docker"),
				Target:   "/work",
				ReadOnly: true,
			}},
			Env: []Env{{Key: "HOME", Value: "/tmp/shipyard-home"}},
		})
		if err != nil || id != "cid-test" {
			t.Fatalf("%s create: id=%q err=%v", provider.Name(), id, err)
		}
		bin, args, err := provider.ExecArgs(context.Background(), ExecRequest{
			ID:      id,
			Command: []string{"codex", "exec"},
			EnvFile: filepath.Join(dir, "env.file"),
			Workdir: "/work",
			User:    "1:1",
		})
		if err != nil {
			t.Fatal(err)
		}
		if bin != provider.Name() {
			t.Fatalf("exec binary = %s, want %s", bin, provider.Name())
		}
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "exec --interactive --user 1:1 --workdir /work --env-file "+filepath.Join(dir, "env.file")+" "+id+" -- codex exec") {
			t.Fatalf("exec args = %s", joined)
		}
	}
	log, err := os.ReadFile(filepath.Join(dir, "calls.log"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(log)
	for _, leaked := range []string{"postgres://service-secret", "master-key", "user:pass@proxy", "DATABASE_URL=", "SHIPYARD_SECRET_KEY=", "HTTP_PROXY="} {
		if strings.Contains(text, leaked) {
			t.Fatalf("runtime CLI inherited %q:\n%s", leaked, text)
		}
	}
	if !strings.Contains(text, "docker\nversion") || !strings.Contains(text, "podman\nversion") {
		t.Fatalf("both binaries must be invoked:\n%s", text)
	}
	if strings.Contains(text, "--privileged") || strings.Contains(text, "docker.sock") || strings.Contains(text, "--network host") {
		t.Fatalf("unsafe runtime flags:\n%s", text)
	}
	if !strings.Contains(text, "readonly") || !strings.Contains(text, "--network\nnone") || !strings.Contains(text, "no-new-privileges") {
		t.Fatalf("expected isolation flags missing:\n%s", text)
	}
}

func TestProviderRejectsHostNetworkSocketAndRootMounts(t *testing.T) {
	provider := NewDocker()
	_, err := provider.Create(context.Background(), Spec{Image: "example/agent:1", Network: "host"})
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("host network was accepted: %v", err)
	}
	_, err = provider.Create(context.Background(), Spec{
		Image:   "example/agent:1",
		Network: "bridge",
		Mounts:  []Mount{{Source: "/", Target: "/host"}},
	})
	if err == nil {
		t.Fatal("root bind was accepted")
	}
	_, err = provider.Create(context.Background(), Spec{
		Image:   "example/agent:1",
		Network: "none",
		Mounts:  []Mount{{Source: "/var/run/docker.sock", Target: "/var/run/docker.sock"}},
	})
	if err == nil || !strings.Contains(err.Error(), "docker socket") {
		t.Fatalf("docker.sock was accepted: %v", err)
	}
}

func TestCreateRecreatesDirectoryBindAfterImageBuild(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "agent-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	dockerfile := filepath.Join(dir, "Dockerfile")
	if err := os.WriteFile(dockerfile, []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeBuildRaceRuntime(t, dir, home)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	provider := NewDocker()
	_, err := provider.Create(context.Background(), Spec{
		Image:      "shipyard/agent-fallback:1",
		Dockerfile: dockerfile,
		ContextDir: dir,
		Network:    "none",
		Mounts:     []Mount{{Source: home, Target: "/tmp/shipyard-home"}},
		BeforeCreate: func() error {
			return os.MkdirAll(home, 0o700)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(home)
	if err != nil || !info.IsDir() {
		t.Fatalf("agent home was not recreated before create: %v", err)
	}
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	_, err = provider.Create(context.Background(), Spec{
		Image:      "shipyard/agent-fallback:1",
		Dockerfile: dockerfile,
		ContextDir: dir,
		Network:    "none",
		Mounts:     []Mount{{Source: home, Target: "/tmp/shipyard-home"}},
	})
	if err == nil || !strings.Contains(err.Error(), "bind source path does not exist") {
		t.Fatalf("missing bind source was accepted: %v", err)
	}
	log, err := os.ReadFile(filepath.Join(dir, "calls.log"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(log)
	if strings.Count(text, "create\n") != 1 {
		t.Fatalf("docker create ran %d times, want 1 (the recreated home):\n%s", strings.Count(text, "create\n"), text)
	}
}

func TestFallbackDockerfileMatchesDeployCopy(t *testing.T) {
	deploy := filepath.Join("..", "..", "deploy", "agent-container", "Dockerfile")
	body, err := os.ReadFile(deploy)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(FallbackDockerfile()) {
		t.Fatal("deploy/agent-container/Dockerfile drifted from the embedded fallback image")
	}
	if !strings.Contains(string(body), "GO_VERSION=1.26.8") || !strings.Contains(string(body), "NODE_VERSION=22.23.2") {
		t.Fatal("fallback image lost the Dev Container toolchain pins")
	}
}

func writeFakeRuntime(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	logPath := filepath.Join(dir, "calls.log")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
{
  printf '%%s\n' "$0"
  printf '%%s\n' "$@"
  echo '--- env ---'
  env | sort
  echo '--- end ---'
} >> %q`, logPath) + `
case "${1:-}" in
  version) echo fake; exit 0 ;;
  create) echo cid-test; exit 0 ;;
esac
exit 0
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeBuildRaceRuntime(t *testing.T, dir, home string) {
	t.Helper()
	logPath := filepath.Join(dir, "calls.log")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
{
  printf '%%s\n' "$@"
  echo '---'
} >> %q
case "${1:-}" in
  version) echo fake; exit 0 ;;
  build) rm -rf %q; exit 0 ;;
  create)
    if [ ! -d %q ]; then
      echo "bind source path does not exist: %s" >&2
      exit 1
    fi
    echo cid-test
    exit 0
    ;;
esac
exit 0
`, logPath, home, home, home)
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
