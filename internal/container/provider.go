// Package container is the agent-run isolation boundary. Call sites depend on
// Provider, not on a Docker-specific type, so Podman (and later runtimes) can
// be selected without changing the worker.
package container

import "context"

// Mount is a container filesystem mount. Bind sources must be absolute,
// non-root host paths. The provider rejects the Docker socket and a source
// of "/".
type Mount struct {
	// Type is "bind" or "tmpfs". Empty means bind.
	Type     string
	Source   string
	Target   string
	ReadOnly bool
}

// Env is a single environment entry. Values are never written to logs by
// this package; callers must keep secrets out of Spec.Env and pass them only
// through an exec env-file.
type Env struct {
	Key   string
	Value string
}

// Spec is a runtime-neutral container to create. Network is only "none" or
// "bridge". There is no privileged mode, capability, or host-namespace field.
type Spec struct {
	Name       string
	Image      string
	Dockerfile string
	ContextDir string
	Workdir    string
	User       string
	Mounts     []Mount
	Env        []Env
	Network    string
	Labels     map[string]string
	Command    []string
}

// ExecRequest runs a process inside an existing container. EnvFile, when set,
// is a host path passed to the runtime as a file so secret values are not
// placed on the process argument list.
type ExecRequest struct {
	ID      string
	Command []string
	EnvFile string
	Workdir string
	User    string
}

// Provider creates and executes agent containers. Docker is the first
// implementation. Podman uses the same Docker-compatible CLI subset.
type Provider interface {
	Name() string
	Available(ctx context.Context) error
	Create(ctx context.Context, spec Spec) (string, error)
	Start(ctx context.Context, id string) error
	ExecArgs(ctx context.Context, req ExecRequest) (string, []string, error)
	Exec(ctx context.Context, req ExecRequest) ([]byte, error)
	Stop(ctx context.Context, id string) error
	Remove(ctx context.Context, id string) error
	Logs(ctx context.Context, id string) ([]byte, error)
	CopyTo(ctx context.Context, id, hostPath, containerPath string) error
}
