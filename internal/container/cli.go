package container

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// cliProvider speaks the Docker-compatible CLI. Docker and Podman are two
// binaries behind the same Provider implementation.
type cliProvider struct {
	binary string
	name   string
}

// NewDocker returns the Docker provider.
func NewDocker() Provider {
	return &cliProvider{binary: "docker", name: "docker"}
}

// NewPodman returns the Podman provider. Podman is invoked with the same
// create/start/exec/stop/rm/logs/cp/build subset as Docker, so call sites do
// not grow a second runtime model. Select it with
// SHIPYARD_CONTAINER_RUNTIME=podman.
func NewPodman() Provider {
	return &cliProvider{binary: "podman", name: "podman"}
}

func (p *cliProvider) Name() string { return p.name }

func (p *cliProvider) Available(ctx context.Context) error {
	if _, err := exec.LookPath(p.binary); err != nil {
		return fmt.Errorf("container runtime %q was not found", p.name)
	}
	out, err := p.run(ctx, "version")
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = err.Error()
		}
		return fmt.Errorf("container runtime %q is not usable: %s", p.name, detail)
	}
	return nil
}

func (p *cliProvider) Create(ctx context.Context, spec Spec) (string, error) {
	if err := validateSpec(spec); err != nil {
		return "", err
	}
	image := spec.Image
	if strings.TrimSpace(spec.Dockerfile) != "" {
		tag := image
		if tag == "" {
			return "", errors.New("container build requires an image tag")
		}
		contextDir := spec.ContextDir
		if contextDir == "" {
			contextDir = filepath.Dir(spec.Dockerfile)
		}
		if _, err := p.run(ctx, "build", "--tag", tag, "--file", spec.Dockerfile, contextDir); err != nil {
			return "", fmt.Errorf("container image build failed: %w", err)
		}
		image = tag
	}
	// The image build can take long enough for a directory bind to disappear.
	// BeforeCreate puts it back. A path that still does not exist is refused
	// here, before dockerd reports an invalid mount.
	if spec.BeforeCreate != nil {
		if err := spec.BeforeCreate(); err != nil {
			return "", fmt.Errorf("container bind source is not ready: %w", err)
		}
	}
	if err := bindSourcesReady(spec.Mounts); err != nil {
		return "", err
	}
	args := []string{"create", "--network", spec.Network, "--security-opt", "no-new-privileges"}
	if spec.Name != "" {
		args = append(args, "--name", spec.Name)
	}
	if spec.User != "" {
		args = append(args, "--user", spec.User)
	}
	if spec.Workdir != "" {
		args = append(args, "--workdir", spec.Workdir)
	}
	labels := make([]string, 0, len(spec.Labels))
	for key, value := range spec.Labels {
		labels = append(labels, key+"="+value)
	}
	sort.Strings(labels)
	for _, label := range labels {
		args = append(args, "--label", label)
	}
	env := append([]Env(nil), spec.Env...)
	sort.Slice(env, func(i, j int) bool { return env[i].Key < env[j].Key })
	for _, entry := range env {
		if err := validateEnvKey(entry.Key); err != nil {
			return "", err
		}
		args = append(args, "--env", entry.Key+"="+entry.Value)
	}
	for _, mount := range spec.Mounts {
		rendered, err := renderMount(mount)
		if err != nil {
			return "", err
		}
		args = append(args, "--mount", rendered)
	}
	args = append(args, image)
	command := spec.Command
	if len(command) == 0 {
		command = []string{"sleep", "infinity"}
	}
	args = append(args, command...)
	out, err := p.run(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("container create failed: %w", err)
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		return "", errors.New("container create returned no id")
	}
	if i := strings.IndexAny(id, "\r\n"); i >= 0 {
		id = strings.TrimSpace(id[:i])
	}
	return id, nil
}

func (p *cliProvider) Start(ctx context.Context, id string) error {
	if _, err := p.run(ctx, "start", id); err != nil {
		return fmt.Errorf("container start failed: %w", err)
	}
	return nil
}

func (p *cliProvider) ExecArgs(_ context.Context, req ExecRequest) (string, []string, error) {
	if strings.TrimSpace(req.ID) == "" {
		return "", nil, errors.New("container id is missing")
	}
	if len(req.Command) == 0 || strings.TrimSpace(req.Command[0]) == "" {
		return "", nil, errors.New("container exec command is missing")
	}
	args := []string{"exec", "--interactive"}
	if req.User != "" {
		args = append(args, "--user", req.User)
	}
	if req.Workdir != "" {
		args = append(args, "--workdir", req.Workdir)
	}
	if req.EnvFile != "" {
		args = append(args, "--env-file", req.EnvFile)
	}
	args = append(args, req.ID, "--")
	args = append(args, req.Command...)
	return p.binary, args, nil
}

func (p *cliProvider) Exec(ctx context.Context, req ExecRequest) ([]byte, error) {
	bin, args, err := p.ExecArgs(ctx, req)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = cliEnvironment()
	out, err := cmd.CombinedOutput()
	if err != nil {
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			return out, err
		}
		return out, fmt.Errorf("%w: %s", err, detail)
	}
	return out, nil
}

func (p *cliProvider) Stop(ctx context.Context, id string) error {
	_, err := p.run(ctx, "stop", "--time", "5", id)
	if err != nil && !missingContainer(err) {
		return fmt.Errorf("container stop failed: %w", err)
	}
	return nil
}

func (p *cliProvider) Remove(ctx context.Context, id string) error {
	_, err := p.run(ctx, "rm", "--force", id)
	if err != nil && !missingContainer(err) {
		return fmt.Errorf("container remove failed: %w", err)
	}
	return nil
}

func (p *cliProvider) Logs(ctx context.Context, id string) ([]byte, error) {
	out, err := p.run(ctx, "logs", id)
	if err != nil {
		return out, fmt.Errorf("container logs failed: %w", err)
	}
	return out, nil
}

func (p *cliProvider) CopyTo(ctx context.Context, id, hostPath, containerPath string) error {
	if _, err := p.run(ctx, "cp", hostPath, id+":"+containerPath); err != nil {
		return fmt.Errorf("container copy failed: %w", err)
	}
	return nil
}

func (p *cliProvider) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, p.binary, args...)
	cmd.Env = cliEnvironment()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if detail == "" {
			detail = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("%s", detail)
	}
	return stdout.Bytes(), nil
}

// HostEnvironment is the narrow environment for the runtime CLI on the host.
// It is not the environment of the agent process inside the container.
func HostEnvironment() []string { return cliEnvironment() }

func cliEnvironment() []string {
	// The runtime CLI must not inherit the Shipyard service environment.
	// Image pulls use the daemon configuration. Proxy credentials, the
	// database URL, and the secret master key stay on the host.
	keys := []string{"PATH", "HOME", "DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_CONFIG", "CONTAINER_HOST", "XDG_RUNTIME_DIR", "TMPDIR", "LANG", "LC_ALL", "USER"}
	env := make([]string, 0, len(keys)+2)
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok && value != "" {
			env = append(env, key+"="+value)
		}
	}
	return append(env, "NO_COLOR=1", "TERM=dumb")
}

func validateSpec(spec Spec) error {
	switch spec.Network {
	case "none", "bridge":
	default:
		return fmt.Errorf("container network %q is not allowed", spec.Network)
	}
	if strings.TrimSpace(spec.Image) == "" && strings.TrimSpace(spec.Dockerfile) == "" {
		return errors.New("container image is missing")
	}
	if strings.Contains(spec.Image, "\n") || strings.Contains(spec.Name, "\n") {
		return errors.New("container image metadata is invalid")
	}
	for _, mount := range spec.Mounts {
		if err := validateMount(mount); err != nil {
			return err
		}
	}
	for _, env := range spec.Env {
		if err := validateEnvKey(env.Key); err != nil {
			return err
		}
	}
	return nil
}

func validateMount(mount Mount) error {
	kind := mount.Type
	if kind == "" {
		kind = "bind"
	}
	target := strings.TrimSpace(mount.Target)
	if target == "" || target == "/" || !strings.HasPrefix(target, "/") {
		return errors.New("container mount target must be an absolute non-root path")
	}
	if strings.Contains(target, "docker.sock") || strings.Contains(mount.Source, "docker.sock") {
		return errors.New("docker socket mounts are not allowed")
	}
	if strings.Contains(target, ",") || strings.Contains(mount.Source, ",") {
		return errors.New("container mount paths must not contain commas")
	}
	switch kind {
	case "tmpfs":
		return nil
	case "bind":
		source := filepath.Clean(strings.TrimSpace(mount.Source))
		if source == "" || source == "/" || !filepath.IsAbs(source) {
			return errors.New("container bind source must be an absolute non-root path")
		}
		return nil
	default:
		return fmt.Errorf("container mount type %q is not allowed", kind)
	}
}

func validateEnvKey(key string) error {
	if key == "" || strings.ContainsAny(key, "=\n\r") {
		return errors.New("container environment name is invalid")
	}
	return nil
}

func renderMount(mount Mount) (string, error) {
	if err := validateMount(mount); err != nil {
		return "", err
	}
	kind := mount.Type
	if kind == "" {
		kind = "bind"
	}
	if kind == "tmpfs" {
		return "type=tmpfs,destination=" + mount.Target, nil
	}
	rendered := fmt.Sprintf("type=bind,source=%s,target=%s", filepath.Clean(mount.Source), mount.Target)
	if mount.ReadOnly {
		rendered += ",readonly"
	}
	return rendered, nil
}

func bindSourcesReady(mounts []Mount) error {
	for _, mount := range mounts {
		kind := mount.Type
		if kind == "" {
			kind = "bind"
		}
		if kind != "bind" {
			continue
		}
		source := filepath.Clean(strings.TrimSpace(mount.Source))
		if _, err := os.Lstat(source); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("bind source path does not exist: %s", source)
			}
			return fmt.Errorf("bind source path is not available: %s", source)
		}
	}
	return nil
}

func missingContainer(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "no such container") || strings.Contains(text, "no container with")
}
