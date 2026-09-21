package automation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"taskboard/internal/container"
	"taskboard/internal/domain"
	"taskboard/internal/sandbox"
	"taskboard/internal/workspace"
	"testing"
)

type recordingProvider struct {
	name    string
	specs   []container.Spec
	execs   []container.ExecRequest
	probes  []container.ExecRequest
	execErr error
	started []string
	// before runs at the start of Create, before Spec.BeforeCreate. Tests use
	// it to delete a bind source and prove the create path puts it back.
	before func(*container.Spec) error
}

func (p *recordingProvider) Name() string { return p.name }
func (p *recordingProvider) Available(context.Context) error {
	return nil
}
func (p *recordingProvider) Create(_ context.Context, spec container.Spec) (string, error) {
	if p.before != nil {
		if err := p.before(&spec); err != nil {
			return "", err
		}
	}
	if spec.BeforeCreate != nil {
		if err := spec.BeforeCreate(); err != nil {
			return "", err
		}
	}
	p.specs = append(p.specs, spec)
	return "cid-recorded", nil
}
func (p *recordingProvider) Start(_ context.Context, id string) error {
	p.started = append(p.started, id)
	return nil
}
func (p *recordingProvider) ExecArgs(_ context.Context, req container.ExecRequest) (string, []string, error) {
	p.execs = append(p.execs, req)
	return p.name, append([]string{"exec", "--env-file", req.EnvFile, req.ID}, req.Command...), nil
}
func (p *recordingProvider) Exec(_ context.Context, req container.ExecRequest) ([]byte, error) {
	p.probes = append(p.probes, req)
	if p.execErr != nil {
		return nil, p.execErr
	}
	return []byte("ok"), nil
}
func (p *recordingProvider) Stop(context.Context, string) error   { return nil }
func (p *recordingProvider) Remove(context.Context, string) error { return nil }
func (p *recordingProvider) Logs(context.Context, string) ([]byte, error) {
	return []byte("log"), nil
}
func (p *recordingProvider) CopyTo(context.Context, string, string, string) error { return nil }

func TestStartAgentContainerUsesFallbackAndBindsAuthAndSecrets(t *testing.T) {
	useContainerRuntimeRoot(t)
	worktree := t.TempDir()
	outDir := t.TempDir()
	codexHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(`{"token":"login"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte("model = \"x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("GROK_HOME", t.TempDir())
	t.Setenv("DATABASE_URL", "postgres://service-secret")
	t.Setenv("SHIPYARD_SECRET_KEY", "master-key")
	t.Setenv("HTTP_PROXY", "http://user:pass@proxy.internal:8080")
	t.Setenv("TASKBOARD_MCP_TOKEN", "mcp-credential")
	secretValue := "shipyard-test-secret-value"
	provider := &recordingProvider{name: "docker"}
	session, err := startAgentContainer(context.Background(), agentContainerRequest{
		RunID:         "run-1",
		Worktree:      worktree,
		Policy:        builtinPolicy(t, "strict"),
		Provider:      domain.ProviderSetting{Provider: "codex"},
		Command:       "sh",
		Args:          []string{"-c", "true"},
		ExtraWritable: []string{outDir},
		Secrets: []domain.SecretValue{{
			EnvName: "OPENAI_API_KEY",
			Value:   secretValue,
		}},
		Runtime: provider,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if len(provider.specs) != 1 {
		t.Fatalf("creates = %d", len(provider.specs))
	}
	spec := provider.specs[0]
	if spec.Image != container.FallbackImage || spec.Dockerfile == "" || spec.Network != "none" {
		t.Fatalf("fallback spec = %+v", spec)
	}
	if spec.User == "" || !strings.Contains(spec.User, ":") {
		t.Fatalf("container user = %q", spec.User)
	}
	assertMount(t, spec.Mounts, worktree, false)
	assertMount(t, spec.Mounts, outDir, false)
	assertMount(t, spec.Mounts, filepath.Join(codexHome, "auth.json"), true)
	assertMount(t, spec.Mounts, filepath.Join(codexHome, "config.toml"), true)
	for _, mount := range spec.Mounts {
		if mount.Source == codexHome {
			t.Fatal("host CLI home was mounted as a whole")
		}
		if strings.Contains(mount.Source, "docker.sock") || mount.Source == "/" {
			t.Fatalf("unsafe mount %+v", mount)
		}
	}
	for _, env := range spec.Env {
		if env.Key == "OPENAI_API_KEY" || env.Key == "DATABASE_URL" || env.Key == "SHIPYARD_SECRET_KEY" || strings.Contains(env.Value, secretValue) || strings.Contains(env.Value, "service-secret") {
			t.Fatalf("create env leaked %s", env.Key)
		}
	}
	if !session.ModelAPIProxy || !strings.Contains(session.IsolationLog, "Isolation=container") || !strings.Contains(session.IsolationLog, "Runtime=docker") {
		t.Fatalf("session log = %q proxy=%v", session.IsolationLog, session.ModelAPIProxy)
	}
	if !strings.Contains(session.SourceLog, "generischer Fallback") {
		t.Fatalf("source = %s", session.SourceLog)
	}
	if session.HostAuthLog != hostCLIAuthMountedLog+"Codex" {
		t.Fatalf("auth log = %q", session.HostAuthLog)
	}
	if len(provider.execs) != 1 {
		t.Fatalf("execs = %+v", provider.execs)
	}
	envText := string(readTestFile(t, provider.execs[0].EnvFile))
	if !strings.Contains(envText, "OPENAI_API_KEY="+secretValue) {
		t.Fatal("assigned secret was not bound into the container env-file")
	}
	if !strings.Contains(envText, "TASKBOARD_MCP_TOKEN=mcp-credential") {
		t.Fatal("explicit MCP credential was not bound")
	}
	if !strings.Contains(envText, "CODEX_HOME="+containerAgentHome+"/.codex") {
		t.Fatalf("codex home was not redirected: %s", envText)
	}
	for _, leaked := range []string{"DATABASE_URL=", "SHIPYARD_SECRET_KEY=", "HTTP_PROXY=", "service-secret", "master-key", "user:pass@proxy"} {
		if strings.Contains(envText, leaked) {
			t.Fatalf("env-file inherited %q:\n%s", leaked, envText)
		}
	}
	joined := strings.Join(session.Args, " ")
	if strings.Contains(joined, secretValue) || strings.Contains(session.Command, secretValue) {
		t.Fatal("secret value leaked into the exec invocation")
	}
	for _, entry := range session.HostEnv {
		if strings.Contains(entry, secretValue) || strings.HasPrefix(entry, "DATABASE_URL=") || strings.HasPrefix(entry, "OPENAI_API_KEY=") {
			t.Fatalf("host env leaked %s", entry)
		}
	}
	info, err := os.Stat(provider.execs[0].EnvFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("env-file mode = %o", info.Mode().Perm())
	}
	assertDurableRuntimePath(t, provider.execs[0].EnvFile)
	assertDurableRuntimePath(t, spec.ContextDir)
	for _, mount := range spec.Mounts {
		switch mount.Target {
		case containerAgentHome, "/tmp/shipyard-model-api.sock", "/tmp/shipyard-model-api-relay.py":
			assertDurableRuntimePath(t, mount.Source)
		}
	}
}

func TestStartAgentContainerPythonProbeFailureNamesTheImage(t *testing.T) {
	useContainerRuntimeRoot(t)
	worktree := t.TempDir()
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GROK_HOME", t.TempDir())
	cause := errors.New(`exec: "--": executable file not found in $PATH`)
	provider := &recordingProvider{name: "docker", execErr: cause}
	_, err := startAgentContainer(context.Background(), agentContainerRequest{
		RunID:    "run-python",
		Worktree: worktree,
		Policy:   builtinPolicy(t, "strict"),
		Provider: domain.ProviderSetting{Provider: "codex"},
		Command:  "sh",
		Runtime:  provider,
	})
	if err == nil {
		t.Fatal("expected the in-image python3 probe to fail")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Agent-Image") || !strings.Contains(msg, "nicht auf dem Host") {
		t.Fatalf("probe error does not say python3 is required inside the agent image: %s", msg)
	}
	if !strings.Contains(msg, cause.Error()) {
		t.Fatalf("probe error dropped the runtime cause: %s", msg)
	}
	if len(provider.probes) != 1 {
		t.Fatalf("probes = %+v", provider.probes)
	}
	got := strings.Join(provider.probes[0].Command, " ")
	if got != "python3 -c import sys" {
		t.Fatalf("probe command = %q", got)
	}
}

func TestStartAgentContainerMountsReviewWorktreeReadOnly(t *testing.T) {
	useContainerRuntimeRoot(t)
	worktree := t.TempDir()
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GROK_HOME", t.TempDir())
	provider := &recordingProvider{name: "docker"}
	policy := reviewAttachmentSandbox(builtinPolicy(t, "development"))
	session, err := startAgentContainer(context.Background(), agentContainerRequest{
		RunID:    "review-1",
		Worktree: worktree,
		Policy:   policy,
		Provider: domain.ProviderSetting{Provider: "codex"},
		Command:  "sh",
		Runtime:  provider,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if policy.WriteMode != "readonly" {
		t.Fatalf("review policy = %+v", policy)
	}
	assertMount(t, provider.specs[0].Mounts, worktree, true)
	if !strings.Contains(session.SourceLog, "nur lesend") {
		t.Fatalf("source = %s", session.SourceLog)
	}
	if provider.specs[0].Network != "none" {
		t.Fatalf("network = %s", provider.specs[0].Network)
	}
}

func TestStartAgentContainerUsesProjectImageAndComposeDockerfile(t *testing.T) {
	useContainerRuntimeRoot(t)
	imageRoot := writeDevcontainer(t, `{"image":"mcr.microsoft.com/devcontainers/base:ubuntu"}`)
	imageDef, found, err := discoverDevContainer(imageRoot)
	if err != nil || !found {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GROK_HOME", t.TempDir())
	provider := &recordingProvider{name: "podman"}
	session, err := startAgentContainer(context.Background(), agentContainerRequest{
		RunID:           "run-image",
		Worktree:        imageRoot,
		Policy:          sandbox.Profile{Name: "qa-network", Description: "test", Mounts: []string{"worktree"}, NetworkMode: "qa-network", WriteMode: "readonly", Active: true},
		Provider:        domain.ProviderSetting{Provider: "codex"},
		Command:         "sh",
		Definition:      imageDef,
		HasDevContainer: true,
		Runtime:         provider,
	})
	if err != nil {
		t.Fatal(err)
	}
	session.Close()
	spec := provider.specs[0]
	if spec.Image != "mcr.microsoft.com/devcontainers/base:ubuntu" || spec.Dockerfile != "" || spec.Network != "bridge" {
		t.Fatalf("image spec = %+v", spec)
	}
	assertMount(t, spec.Mounts, imageRoot, true)
	if session.ModelAPIProxy {
		t.Fatal("qa-network must not install the model-api proxy")
	}

	composeRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(composeRoot, ".devcontainer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(composeRoot, ".devcontainer", "Dockerfile"), []byte("FROM ubuntu:24.04\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	compose := "services:\n  dev:\n    image: shipyard-dev:local\n    build:\n      context: .\n      dockerfile: Dockerfile\n    environment:\n      DATABASE_URL: postgres://taskboard:taskboard@postgres:5432/taskboard\n"
	if err := os.WriteFile(filepath.Join(composeRoot, ".devcontainer", "docker-compose.yml"), []byte(compose), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(composeRoot, ".devcontainer", "devcontainer.json"), []byte(`{"dockerComposeFile":"docker-compose.yml","service":"dev","features":{"ghcr.io/devcontainers/features/go:1":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	composeDef, found, err := discoverDevContainer(composeRoot)
	if err != nil || !found {
		t.Fatal(err)
	}
	provider = &recordingProvider{name: "docker"}
	session, err = startAgentContainer(context.Background(), agentContainerRequest{
		RunID:           "run-compose",
		Worktree:        composeRoot,
		Policy:          builtinPolicy(t, "development"),
		Provider:        domain.ProviderSetting{Provider: "codex"},
		Command:         "sh",
		Definition:      composeDef,
		HasDevContainer: true,
		Runtime:         provider,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	spec = provider.specs[0]
	if spec.Image != "shipyard-dev:local" || spec.Dockerfile != filepath.Join(composeRoot, ".devcontainer", "Dockerfile") {
		t.Fatalf("compose spec image=%s dockerfile=%s", spec.Image, spec.Dockerfile)
	}
	if spec.ContextDir != filepath.Join(composeRoot, ".devcontainer") {
		t.Fatalf("context = %s", spec.ContextDir)
	}
	assertMount(t, spec.Mounts, composeRoot, false)
	for _, env := range spec.Env {
		if env.Key == "DATABASE_URL" || strings.Contains(env.Value, "taskboard:taskboard") {
			t.Fatal("compose environment was copied into the run container")
		}
	}
	if len(provider.execs) != 1 {
		t.Fatal("missing exec")
	}
	envText := string(readTestFile(t, provider.execs[0].EnvFile))
	if strings.Contains(envText, "DATABASE_URL=") || strings.Contains(envText, "taskboard:taskboard") {
		t.Fatalf("compose environment reached the env-file:\n%s", envText)
	}
	if !strings.Contains(session.SourceLog, "Compose-Service dev") || !strings.Contains(session.SourceLog, "Features werden nicht installiert") {
		t.Fatalf("source = %s", session.SourceLog)
	}
}

func TestStartAgentContainerRejectsDisallowedDevcontainerBeforeCreate(t *testing.T) {
	root := writeDevcontainer(t, `{"image":"alpine:3","privileged":true}`)
	def, found, err := discoverDevContainer(root)
	if err != nil || !found {
		t.Fatal(err)
	}
	provider := &recordingProvider{name: "docker"}
	_, err = startAgentContainer(context.Background(), agentContainerRequest{
		RunID:           "run-deny",
		Worktree:        root,
		Policy:          builtinPolicy(t, "strict"),
		Command:         "sh",
		Definition:      def,
		HasDevContainer: true,
		Runtime:         provider,
	})
	if err == nil || !strings.Contains(err.Error(), "privileg") {
		t.Fatalf("privileged definition was accepted: %v", err)
	}
	if len(provider.specs) != 0 {
		t.Fatal("disallowed definition reached the container runtime")
	}
}

func TestStartAgentContainerBlocksReleaseBridge(t *testing.T) {
	provider := &recordingProvider{name: "docker"}
	_, err := startAgentContainer(context.Background(), agentContainerRequest{
		RunID:    "run-bridge",
		Worktree: t.TempDir(),
		Policy:   builtinPolicy(t, "release-bridge"),
		Command:  "sh",
		Runtime:  provider,
	})
	if err == nil || !strings.Contains(err.Error(), "Release-Bridge") {
		t.Fatalf("release-bridge was not blocked: %v", err)
	}
	if len(provider.specs) != 0 {
		t.Fatal("release-bridge created a container")
	}
}

func TestRepositoryDevcontainerPlansTheComposeDockerfile(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	def, found, err := discoverDevContainer(root)
	if err != nil || !found {
		t.Fatal(err)
	}
	plan, err := planContainerSource(root, def, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Image != "shipyard-dev:local" || plan.Kind != "project-compose" {
		t.Fatalf("plan = %+v", plan)
	}
	if plan.Dockerfile != filepath.Join(root, ".devcontainer", "Dockerfile") {
		t.Fatalf("dockerfile = %s", plan.Dockerfile)
	}
	if plan.Context != filepath.Join(root, ".devcontainer") {
		t.Fatalf("context = %s", plan.Context)
	}
}

func TestContainerRuntimeSelection(t *testing.T) {
	t.Setenv("SHIPYARD_CONTAINER_RUNTIME", "podman")
	podman, err := newRunContainerProvider()
	if err != nil || podman.Name() != "podman" {
		t.Fatalf("podman = %v %v", podman, err)
	}
	t.Setenv("SHIPYARD_CONTAINER_RUNTIME", "docker")
	docker, err := newRunContainerProvider()
	if err != nil || docker.Name() != "docker" {
		t.Fatalf("docker = %v %v", docker, err)
	}
	t.Setenv("SHIPYARD_CONTAINER_RUNTIME", "lxc")
	if _, err := newRunContainerProvider(); err == nil {
		t.Fatal("unknown runtime was accepted")
	}
}

func TestAgentHomeIsRecreatedImmediatelyBeforeCreate(t *testing.T) {
	useContainerRuntimeRoot(t)
	worktree := t.TempDir()
	codexHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), []byte(`{"token":"login"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("GROK_HOME", t.TempDir())
	var home string
	provider := &recordingProvider{name: "docker"}
	provider.before = func(spec *container.Spec) error {
		for _, mount := range spec.Mounts {
			if mount.Target != containerAgentHome {
				continue
			}
			home = mount.Source
			if err := os.RemoveAll(mount.Source); err != nil {
				return err
			}
			if _, err := os.Stat(mount.Source); !os.IsNotExist(err) {
				return fmt.Errorf("agent home still exists after simulated cleanup: %v", err)
			}
		}
		if home == "" {
			return fmt.Errorf("agent home mount missing")
		}
		return nil
	}
	session, err := startAgentContainer(context.Background(), agentContainerRequest{
		RunID:    "run-recreate",
		Worktree: worktree,
		Policy:   builtinPolicy(t, "strict"),
		Provider: domain.ProviderSetting{Provider: "codex"},
		Command:  "sh",
		Runtime:  provider,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	info, err := os.Stat(home)
	if err != nil || !info.IsDir() {
		t.Fatalf("agent home was not recreated before create: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("agent home mode = %o", info.Mode().Perm())
	}
	for _, dir := range []string{".config", ".cache", ".codex"} {
		if _, err := os.Stat(filepath.Join(home, dir)); err != nil {
			t.Fatalf("recreated home missing %s: %v", dir, err)
		}
	}
	assertDurableRuntimePath(t, home)
}

func TestStartAgentContainerDockerCLIOmitsServiceEnvironment(t *testing.T) {
	useContainerRuntimeRoot(t)
	dir := t.TempDir()
	writeFakeContainerCLI(t, dir, "docker")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SHIPYARD_CONTAINER_RUNTIME", "docker")
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("GROK_HOME", t.TempDir())
	t.Setenv("DATABASE_URL", "postgres://service-secret")
	t.Setenv("SHIPYARD_SECRET_KEY", "master-key")
	secretValue := "shipyard-test-secret-value"
	worktree := t.TempDir()
	session, err := startAgentContainer(context.Background(), agentContainerRequest{
		RunID:    "run-cli",
		Worktree: worktree,
		Policy:   builtinPolicy(t, "qa-readonly"),
		Provider: domain.ProviderSetting{Provider: "codex"},
		Command:  "sh",
		Secrets:  []domain.SecretValue{{EnvName: "OPENAI_API_KEY", Value: secretValue}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	log := string(readTestFile(t, filepath.Join(dir, "calls.log")))
	if strings.Contains(log, secretValue) || strings.Contains(log, "service-secret") || strings.Contains(log, "master-key") || strings.Contains(log, "DATABASE_URL=") {
		t.Fatalf("docker CLI saw a secret or service env:\n%s", log)
	}
	if !strings.Contains(log, "readonly") || !strings.Contains(log, "--network\nnone") {
		t.Fatalf("docker CLI missed isolation flags:\n%s", log)
	}
	if !strings.Contains(strings.Join(session.Args, " "), "--env-file") || strings.Contains(strings.Join(session.Args, " "), secretValue) {
		t.Fatalf("exec args = %v", session.Args)
	}
	if !strings.Contains(session.SourceLog, "nur lesend") || !strings.Contains(session.IsolationLog, "Isolation=container") {
		t.Fatalf("logs = %s | %s", session.IsolationLog, session.SourceLog)
	}
}

func useContainerRuntimeRoot(t *testing.T) string {
	t.Helper()
	// A short directory keeps the model-API unix socket under sun_path's 108
	// byte limit. t.TempDir() embeds the full test name and makes that path
	// too long once it sits under runtime/container.
	root, err := os.MkdirTemp("", "sy")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Setenv("TASKBOARD_WORKSPACE_ROOT", root)
	return root
}

func assertDurableRuntimePath(t *testing.T, path string) {
	t.Helper()
	path = filepath.Clean(path)
	root := filepath.Clean(filepath.Join(workspace.RuntimeRoot(), "container"))
	if path != root && !strings.HasPrefix(path, root+string(filepath.Separator)) {
		t.Fatalf("bind source %s is outside the durable runtime directory %s", path, root)
	}
	parent := filepath.Dir(path)
	for _, private := range []string{"/tmp", "/var/tmp", filepath.Clean(os.TempDir())} {
		if path == private || parent == private {
			t.Fatalf("bind source %s is under PrivateTmp path %s", path, private)
		}
	}
	if strings.Contains(filepath.Base(path), "shipyard-agent-home-") && parent == filepath.Clean(os.TempDir()) {
		t.Fatalf("bind source %s still uses a service-private temp home", path)
	}
}

func assertMount(t *testing.T, mounts []container.Mount, source string, readOnly bool) {
	t.Helper()
	source = filepath.Clean(source)
	for _, mount := range mounts {
		if filepath.Clean(mount.Source) == source {
			if mount.ReadOnly != readOnly {
				t.Fatalf("mount %s readonly=%v, want %v", source, mount.ReadOnly, readOnly)
			}
			return
		}
	}
	t.Fatalf("mount %s missing in %+v", source, mounts)
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func writeFakeContainerCLI(t *testing.T, dir, name string) {
	t.Helper()
	logPath := filepath.Join(dir, "calls.log")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
{
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
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}
