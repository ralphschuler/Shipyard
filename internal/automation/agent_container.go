package automation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"taskboard/internal/container"
	"taskboard/internal/domain"
	"taskboard/internal/sandbox"
	"taskboard/internal/workspace"
)

const containerAgentHome = sandboxCLIHome

// modelAPIRelayScriptMode is the mode of the bind-mounted allowlist relay.
// taskboard.service sets UMask=0077, and the script used to be chmod 0500.
// Rootless Docker maps `docker exec --user <uid>:<gid>` (the host ids from
// containerUser) onto a subordinate uid, so that owner-only file is EACCES
// when python opens /tmp/shipyard-model-api-relay.py. The script is embedded
// source. 0644 is readable by that subordinate uid and is not world-writable.
// Chmod is required: WriteFile applies the service umask and would collapse
// 0644 back to 0600.
const modelAPIRelayScriptMode os.FileMode = 0o644

// modelAPIRelaySocketMode is connect permission for the same subordinate uid.
// Write on a unix socket is permission to connect. The socket lives in the
// 0700 container bind directory, so other host users still cannot resolve it.
// The bubblewrap proxy keeps its owner-only socket in the service temp dir.
const modelAPIRelaySocketMode os.FileMode = 0o666

// agentContainerRequest is the production CLI execution boundary. The sandbox
// profile becomes container mounts and network mode. Bubblewrap is not used
// on this path.
type agentContainerRequest struct {
	RunID           string
	Worktree        string
	Policy          sandbox.Profile
	Provider        domain.ProviderSetting
	Command         string
	Args            []string
	ExtraWritable   []string
	Secrets         []domain.SecretValue
	Definition      devContainerConfig
	HasDevContainer bool
	// Runtime overrides provider selection. Tests inject a fake Provider.
	Runtime container.Provider
}

// agentContainerSession is the host-side invocation of a process inside the
// run container. HostEnv is only the runtime CLI environment. Assigned secrets
// live in an env-file passed to exec and are not part of HostEnv or Args.
type agentContainerSession struct {
	Command       string
	Args          []string
	HostEnv       []string
	IsolationLog  string
	HostAuthLog   string
	SourceLog     string
	ModelAPIProxy bool
	closeFns      []func() error
}

func (s *agentContainerSession) Close() error {
	if s == nil {
		return nil
	}
	var errs []error
	for i := len(s.closeFns) - 1; i >= 0; i-- {
		if err := s.closeFns[i](); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type containerSourcePlan struct {
	Kind       string
	Image      string
	Dockerfile string
	Context    string
	Detail     string
}

func newRunContainerProvider() (container.Provider, error) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SHIPYARD_CONTAINER_RUNTIME"))) {
	case "", "docker":
		return container.NewDocker(), nil
	case "podman":
		return container.NewPodman(), nil
	default:
		return nil, errors.New("SHIPYARD_CONTAINER_RUNTIME muss docker oder podman sein")
	}
}

func cliContainerRuntimeAvailable(ctx context.Context) error {
	runtime, err := newRunContainerProvider()
	if err != nil {
		return err
	}
	if err := runtime.Available(ctx); err != nil {
		return errors.New("CLI-Agenten benötigen eine Container-Runtime (docker, oder SHIPYARD_CONTAINER_RUNTIME=podman)")
	}
	return nil
}

// Mount semantics for a run container:
//   - The run worktree is bind-mounted at the same absolute host path so CLI
//     --cd and gitdir pointers stay valid. Review attaches to the Delivery
//     worktree and reviewAttachmentSandbox forces WriteMode=readonly, which
//     mounts that path read-only. A readonly profile does the same.
//   - Git metadata and the module cache stay read-only, matching the previous
//     CLI sandbox. The final-message directory is a separate writable mount
//     outside the worktree.
//   - Allowlisted host CLI login files are mounted read-only under the
//     container home. The host home directory itself is never mounted.
//     Codex, Claude, and Grok config directories that do not exist are
//     skipped. Codex, Claude Code, and the Grok CLI themselves come from
//     the agent image (/usr/local/bin), not from a host binary mount.
//   - Task-assigned secrets are injected through an exec env-file. The
//     container does not receive the Shipyard service environment.
//   - Bind sources Shipyard creates (agent home, secret env-file, model-API
//     socket, and relay script) live under the workspace runtime directory.
//     /tmp and /var/tmp are private to the systemd unit (PrivateTmp=true)
//     and are invisible to rootless dockerd. The agent home is created again
//     immediately before the runtime create call. A project without a Dev
//     Container pulls the published GHCR agent base image; it does not build
//     a local fallback Dockerfile.
func startAgentContainer(ctx context.Context, req agentContainerRequest) (_ *agentContainerSession, retErr error) {
	worktree, err := filepath.Abs(req.Worktree)
	if err != nil {
		return nil, err
	}
	worktree = filepath.Clean(worktree)
	if _, err := sandbox.EffectiveProfile(req.Policy, worktree); err != nil {
		return nil, err
	}
	network, err := containerNetwork(req.Policy.NetworkMode)
	if err != nil {
		return nil, err
	}
	if req.Policy.WriteMode != "worktree" && req.Policy.WriteMode != "readonly" {
		return nil, fmt.Errorf("Sandbox-Schreibmodus %q wird vom Container-Adapter nicht unterstützt", req.Policy.WriteMode)
	}
	if req.HasDevContainer {
		if err := reviewDevContainerPolicy(req.Definition, devContainerApproval{}); err != nil {
			return nil, err
		}
	}
	runtime := req.Runtime
	if runtime == nil {
		runtime, err = newRunContainerProvider()
		if err != nil {
			return nil, err
		}
	}
	if err := runtime.Available(ctx); err != nil {
		return nil, errors.New("CLI-Agenten benötigen eine Container-Runtime (docker, oder SHIPYARD_CONTAINER_RUNTIME=podman)")
	}
	plan, err := planContainerSource(worktree, req.Definition, req.HasDevContainer)
	if err != nil {
		return nil, err
	}
	session := &agentContainerSession{}
	defer func() {
		if retErr != nil {
			_ = session.Close()
		}
	}()
	bindRoot, err := agentContainerBindRoot()
	if err != nil {
		return nil, err
	}
	if plan.Kind == "fallback" {
		image, detail, pullErr := pullAgentBaseImage(ctx, runtime)
		if pullErr != nil {
			return nil, pullErr
		}
		plan.Image = image
		plan.Detail = detail
	}
	cli, err := resolveContainerCLI(req.Command)
	if err != nil {
		return nil, err
	}
	auth := resolveHostCLIAuth(req.Provider.Provider, worktree)
	homeHost, err := os.MkdirTemp(bindRoot, "agent-home-")
	if err != nil {
		return nil, err
	}
	session.closeFns = append(session.closeFns, func() error { return os.RemoveAll(homeHost) })
	if err := ensureAgentHomeLayout(homeHost, auth); err != nil {
		return nil, err
	}
	// Rootless container runtimes remap non-root container UIDs to subordinate
	// host UIDs. Directly bind-mounting a host-owned 0600 auth file therefore
	// makes it unreadable to the container's configured user even when the
	// numeric UID appears to match. Stage the allowlisted files into the
	// per-run home instead: the runtime directory is service-private on the
	// host, while the staged files can use container-readable modes.
	if err := stageHostCLIAuth(homeHost, auth); err != nil {
		return nil, err
	}
	if err := requireDurableBind(homeHost); err != nil {
		return nil, err
	}
	hostMounts, hostEnv := cliSandboxHostMounts(worktree)
	var relaySocket, relayScript string
	if req.Policy.NetworkMode == "none" {
		proxy, proxyErr := startModelAPIProxyIn(bindRoot, modelAPIAllowlist(req.Provider))
		if proxyErr != nil {
			return nil, fmt.Errorf("Modell-API-Proxy konnte nicht gestartet werden: %w", proxyErr)
		}
		session.closeFns = append(session.closeFns, proxy.Close)
		relay, relayErr := os.CreateTemp(bindRoot, "shipyard-model-api-relay-*.py")
		if relayErr != nil {
			return nil, relayErr
		}
		relayScript = relay.Name()
		session.closeFns = append(session.closeFns, func() error { return os.Remove(relayScript) })
		if _, err := relay.Write(modelAPIRelayScript); err != nil {
			_ = relay.Close()
			return nil, err
		}
		if err := relay.Close(); err != nil {
			return nil, err
		}
		if err := publishRelayScript(relayScript); err != nil {
			return nil, err
		}
		relaySocket = proxy.path
		if err := publishContainerRelaySocket(relaySocket); err != nil {
			return nil, err
		}
		if err := requireDurableBind(relayScript); err != nil {
			return nil, err
		}
		if err := requireDurableBind(relaySocket); err != nil {
			return nil, err
		}
		session.ModelAPIProxy = true
	}
	user := containerUser()
	containerAuth := auth
	containerAuth.Binds = nil
	mounts, err := agentContainerMounts(worktree, req.Policy, homeHost, containerAuth, cli.Binds, req.ExtraWritable, hostMounts, relaySocket, relayScript)
	if err != nil {
		return nil, err
	}
	spec := container.Spec{
		Name:       containerName(req.RunID),
		Image:      plan.Image,
		Dockerfile: plan.Dockerfile,
		ContextDir: plan.Context,
		Workdir:    worktree,
		User:       user,
		Mounts:     mounts,
		Env:        containerCreateEnv(auth),
		Network:    network,
		Labels: map[string]string{
			"shipyard.run":  sanitizeLabel(req.RunID),
			"shipyard.role": "agent",
		},
		Command: []string{"sleep", "infinity"},
	}
	spec.BeforeCreate = func() error {
		if err := ensureAgentHomeLayout(homeHost, auth); err != nil {
			return err
		}
		if relayScript != "" {
			if err := ensureRelayScript(relayScript); err != nil {
				return err
			}
		}
		if relaySocket != "" {
			if err := publishContainerRelaySocket(relaySocket); err != nil {
				return err
			}
		}
		return nil
	}
	id, err := runtime.Create(ctx, spec)
	if err != nil {
		return nil, err
	}
	session.closeFns = append(session.closeFns, func() error {
		cleanupCtx := context.WithoutCancel(ctx)
		_ = runtime.Stop(cleanupCtx, id)
		return runtime.Remove(cleanupCtx, id)
	})
	if err := runtime.Start(ctx, id); err != nil {
		return nil, err
	}
	if req.Policy.NetworkMode == "none" {
		if _, err := runtime.Exec(ctx, container.ExecRequest{
			ID:      id,
			Command: []string{"python3", "-c", "import sys"},
			Workdir: "/",
			User:    user,
		}); err != nil {
			return nil, fmt.Errorf("Container-Lauf braucht python3 im Agent-Image für den Modell-API-Allowlist-Proxy (nicht auf dem Host): %w", err)
		}
	}
	if cli.NeedsNode {
		if _, err := runtime.Exec(ctx, container.ExecRequest{
			ID:      id,
			Command: []string{"node", "-e", "process.exit(0)"},
			Workdir: "/",
			User:    user,
		}); err != nil {
			return nil, fmt.Errorf("Container-Lauf braucht node im Agent-Image, um den Host-CLI-Launcher %q zu starten (nicht das Host-Node): %w", req.Command, err)
		}
	}
	envFile := homeHost + ".env"
	if err := requireDurableBind(envFile); err != nil {
		return nil, err
	}
	entries := containerExecEnv(auth, req.Secrets, hostEnv, cli.ExtraPATH, relaySocket != "")
	if err := writeContainerEnvFile(envFile, entries); err != nil {
		return nil, err
	}
	session.closeFns = append(session.closeFns, func() error { return os.Remove(envFile) })
	execCommand := append([]string{}, cli.Argv...)
	agentArgs := append([]string{}, req.Args...)
	// The worktree has already been created and validated by Shipyard before
	// this container is started. In a rootless container Git reports the bind
	// source as owned by a remapped UID, which makes Codex reject it as an
	// untrusted repository even though it is the dedicated run worktree. Scope
	// the bypass to the in-image Codex CLI and this validated container path;
	// do not weaken checks for arbitrary provider commands.
	// Only `codex exec` consults the repository trust check. Keep diagnostic
	// commands such as `codex --version` byte-for-byte unchanged.
	if req.Provider.Provider == "codex" && filepath.Base(cli.Argv[0]) == "codex" && len(agentArgs) > 0 && agentArgs[0] == "exec" {
		agentArgs = withCodexManagedWorktreeTrust(agentArgs)
	}
	execCommand = append(execCommand, agentArgs...)
	if relayScript != "" {
		execCommand = append([]string{"python3", "/tmp/shipyard-model-api-relay.py"}, execCommand...)
	}
	bin, args, err := runtime.ExecArgs(ctx, container.ExecRequest{
		ID:      id,
		Command: execCommand,
		EnvFile: envFile,
		Workdir: worktree,
		User:    user,
	})
	if err != nil {
		return nil, err
	}
	session.Command = bin
	session.Args = args
	session.HostEnv = container.HostEnvironment()
	session.IsolationLog = fmt.Sprintf("Sandbox-Profil wirksam: %s · Netzwerk=%s · Schreiben=%s · Isolation=container · Runtime=%s", req.Policy.Name, req.Policy.NetworkMode, req.Policy.WriteMode, runtime.Name())
	session.HostAuthLog = auth.infoLog()
	session.SourceLog = "Container-Quelle: " + plan.Detail
	if req.HasDevContainer && req.Definition.HasFeatures {
		session.SourceLog += "; Dev-Container-Features werden nicht installiert"
	}
	if req.Policy.WriteMode == "readonly" {
		session.SourceLog += "; Worktree nur lesend eingehängt"
	}
	return session, nil
}

func withCodexManagedWorktreeTrust(args []string) []string {
	for _, arg := range args {
		if arg == "--skip-git-repo-check" {
			return args
		}
	}
	// Codex treats the final "-" as the stdin prompt marker, so every option
	// must precede it.
	for index, arg := range args {
		if arg == "-" {
			trusted := make([]string, 0, len(args)+1)
			trusted = append(trusted, args[:index]...)
			trusted = append(trusted, "--skip-git-repo-check")
			trusted = append(trusted, args[index:]...)
			return trusted
		}
	}
	return append(args, "--skip-git-repo-check")
}

// agentContainerBindRoot is the directory both this process and the container
// runtime can see. It is the workspace runtime root, never the service-private
// temporary directory created by systemd PrivateTmp=true.
func agentContainerBindRoot() (string, error) {
	dir := filepath.Join(workspace.RuntimeRoot(), "container")
	if !filepath.IsAbs(dir) || filepath.Clean(dir) == string(filepath.Separator) {
		return "", errors.New("Container-Bind-Verzeichnis ist ungültig")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("Container-Bind-Verzeichnis ist nicht verfügbar: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", fmt.Errorf("Container-Bind-Verzeichnis ist nicht verfügbar: %w", err)
	}
	return dir, nil
}

func requireDurableBind(path string) error {
	path = filepath.Clean(path)
	root := filepath.Clean(filepath.Join(workspace.RuntimeRoot(), "container"))
	if path != root && !strings.HasPrefix(path, root+string(filepath.Separator)) {
		return fmt.Errorf("container bind source %s liegt außerhalb des Runtime-Verzeichnisses", path)
	}
	parent := filepath.Dir(path)
	for _, private := range []string{"/tmp", "/var/tmp", filepath.Clean(os.TempDir())} {
		if path == private || parent == private {
			return fmt.Errorf("container bind source %s ist für die Container-Runtime nicht sichtbar (PrivateTmp)", path)
		}
	}
	return nil
}

func ensureAgentHomeLayout(homeHost string, auth hostCLIAuthPlan) error {
	if err := os.MkdirAll(homeHost, 0o700); err != nil {
		return err
	}
	// The bind root is already mode 0700 on the host. The per-run home itself
	// must be traversable by a non-root container UID after rootless UID
	// remapping, where the host owner is represented as container root.
	if err := os.Chmod(homeHost, 0o755); err != nil {
		return err
	}
	for _, dir := range auth.DestDirs {
		rel, relErr := filepath.Rel(containerAgentHome, dir)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errors.New("Host-CLI-Login liegt außerhalb des Container-Home")
		}
		// The bind root remains service-private (0700), but every directory
		// below it must be traversable by the container user. Rootless Docker
		// remaps that user to a subordinate host UID, so owner-only (0700)
		// directories here cause CODEX_HOME/CLAUDE_CONFIG_DIR to fail with
		// EACCES even when the staged files themselves are readable.
		if err := os.MkdirAll(filepath.Join(homeHost, rel), 0o755); err != nil {
			return err
		}
		if err := os.Chmod(filepath.Join(homeHost, rel), 0o755); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(homeHost, ".config"), 0o755); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Join(homeHost, ".config"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(homeHost, ".cache"), 0o755); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Join(homeHost, ".cache"), 0o755); err != nil {
		return err
	}
	return nil
}

// stageHostCLIAuth copies only the allowlisted CLI files into the per-run
// home. The runtime directory itself is mode 0700 on the host, so making the
// staged files readable by the container's remapped UID does not expose them
// to unrelated host users. It also avoids nested bind mounts whose parent
// ownership is interpreted differently by rootless Docker/Podman.
func stageHostCLIAuth(homeHost string, auth hostCLIAuthPlan) error {
	for _, bind := range auth.Binds {
		rel, err := filepath.Rel(containerAgentHome, filepath.Clean(bind.Dest))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errors.New("Host-CLI-Login liegt außerhalb des Container-Home")
		}
		if err := copyContainerAuthPath(bind.Source, filepath.Join(homeHost, rel)); err != nil {
			return fmt.Errorf("Host-CLI-Login konnte nicht für den Container vorbereitet werden: %w", err)
		}
	}
	return nil
}

func copyContainerAuthPath(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("symbolische Links in Host-CLI-Login sind nicht erlaubt")
	}
	if info.IsDir() {
		if err := os.MkdirAll(destination, 0o755); err != nil {
			return err
		}
		if err := os.Chmod(destination, 0o755); err != nil {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := copyContainerAuthPath(filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return errors.New("Host-CLI-Login enthält einen nicht unterstützten Dateityp")
	}
	body, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(destination, body, 0o644); err != nil {
		return err
	}
	return os.Chmod(destination, 0o644)
}

func ensureRelayScript(path string) error {
	if _, err := os.Stat(path); err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		// The mode argument is masked by UMask=0077. publishRelayScript
		// chmods the final mode after the bytes are in place.
		if err := os.WriteFile(path, modelAPIRelayScript, modelAPIRelayScriptMode); err != nil {
			return err
		}
	}
	return publishRelayScript(path)
}

func publishRelayScript(path string) error {
	if err := os.Chmod(path, modelAPIRelayScriptMode); err != nil {
		return err
	}
	return alignRelayScriptOwner(path)
}

// alignRelayScriptOwner keeps the host owner aligned with containerUser.
// On a rootful runtime that numeric id is the container user, so owner-read
// works. On rootless the same numbers are a different host uid inside the
// user namespace; modelAPIRelayScriptMode's other-read bit covers that case.
// Ownership is left unchanged when it already matches, because a no-op chown
// can fail on NFS.
func alignRelayScriptOwner(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	uid, gid := os.Getuid(), os.Getgid()
	if int(stat.Uid) == uid && int(stat.Gid) == gid {
		return nil
	}
	if err := os.Chown(path, uid, gid); err != nil {
		return fmt.Errorf("Modell-API-Relay gehört nicht dem Container-Exec-Benutzer: %w", err)
	}
	return nil
}

func publishContainerRelaySocket(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("Modell-API-Proxy-Socket fehlt vor dem Container-Start: %w", err)
	}
	return os.Chmod(path, modelAPIRelaySocketMode)
}

func containerNetwork(mode string) (string, error) {
	switch mode {
	case "none":
		return "none", nil
	case "qa-network":
		return "bridge", nil
	case "bridge-only":
		return "", errors.New("release-bridge erlaubt nur den hostseitigen Release-Bridge-Dienst")
	default:
		return "", fmt.Errorf("Sandbox-Netzwerkmodus %q wird vom Container-Adapter nicht unterstützt", mode)
	}
}

func containerUser() string {
	return strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())
}

func containerName(runID string) string {
	var b strings.Builder
	b.WriteString("shipyard-agent-")
	for _, r := range runID {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		}
	}
	if b.Len() == len("shipyard-agent-") {
		b.WriteString("run")
	}
	return b.String()
}

func sanitizeLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "run"
	}
	return strings.ReplaceAll(value, "\n", "")
}

func pullAgentBaseImage(ctx context.Context, runtime container.Provider) (image, detail string, err error) {
	primary := container.AgentBaseImage()
	if pullErr := runtime.Pull(ctx, primary); pullErr == nil {
		return primary, "generischer Fallback (" + primary + ")", nil
	} else if primary == container.AgentBaseLatestImage() {
		return "", "", fmt.Errorf("Agent-Basisimage %s konnte nicht gezogen werden: %w", primary, pullErr)
	} else {
		latest := container.AgentBaseLatestImage()
		if latestErr := runtime.Pull(ctx, latest); latestErr != nil {
			return "", "", fmt.Errorf("Agent-Basisimage %s konnte nicht gezogen werden (%v); %s ebenfalls nicht: %w", primary, pullErr, latest, latestErr)
		}
		return latest, "generischer Fallback (" + latest + ", Release-Tag nicht verfügbar)", nil
	}
}

func planContainerSource(worktree string, def devContainerConfig, has bool) (containerSourcePlan, error) {
	if !has {
		image := container.AgentBaseImage()
		return containerSourcePlan{Kind: "fallback", Image: image, Detail: "generischer Fallback (" + image + ")"}, nil
	}
	if def.Dockerfile != "" && len(def.ComposeFiles) == 0 {
		tag := strings.TrimSpace(def.Image)
		if tag == "" {
			tag = projectImageTag(def)
		}
		return containerSourcePlan{
			Kind:       "project-dockerfile",
			Image:      tag,
			Dockerfile: def.Dockerfile,
			Context:    worktree,
			Detail:     "Projekt-Dev-Container Dockerfile",
		}, nil
	}
	if len(def.ComposeFiles) > 0 {
		return planComposeSource(worktree, def)
	}
	if image := strings.TrimSpace(def.Image); image != "" {
		return containerSourcePlan{Kind: "project-image", Image: image, Detail: "Projekt-Dev-Container Image " + image}, nil
	}
	return containerSourcePlan{}, errors.New("Dev-Container-Definition enthält kein startbares Image oder Dockerfile")
}

func planComposeSource(worktree string, def devContainerConfig) (containerSourcePlan, error) {
	service := strings.TrimSpace(def.definition.Service)
	if service == "" {
		return containerSourcePlan{}, errors.New("Dev-Container-Compose nennt keinen service")
	}
	var found bool
	var image, contextDir, dockerfile string
	for _, file := range def.ComposeFiles {
		data, err := os.ReadFile(file)
		if err != nil {
			return containerSourcePlan{}, fmt.Errorf("dockerComposeFile nicht gefunden: %w", err)
		}
		parsedImage, parsedContext, parsedDockerfile, ok, err := parseComposeService(string(data), service)
		if err != nil {
			return containerSourcePlan{}, err
		}
		if !ok {
			continue
		}
		found = true
		if parsedImage != "" {
			image = parsedImage
		}
		if parsedDockerfile != "" {
			base := filepath.Dir(file)
			ctx := parsedContext
			if ctx == "" {
				ctx = "."
			}
			if !filepath.IsAbs(ctx) {
				ctx = filepath.Join(base, ctx)
			}
			contextDir = filepath.Clean(ctx)
			df := parsedDockerfile
			if !filepath.IsAbs(df) {
				df = filepath.Join(contextDir, df)
			}
			dockerfile = filepath.Clean(df)
		}
	}
	if !found {
		return containerSourcePlan{}, fmt.Errorf("Dev-Container-Compose enthält den Service %q nicht", service)
	}
	if dockerfile != "" {
		if !pathWithin(worktree, dockerfile) || !pathWithin(worktree, contextDir) {
			return containerSourcePlan{}, errors.New("Dev-Container-Buildkontext liegt außerhalb des Workspaces")
		}
		tag := image
		if tag == "" {
			tag = projectImageTag(def)
		}
		return containerSourcePlan{
			Kind:       "project-compose",
			Image:      tag,
			Dockerfile: dockerfile,
			Context:    contextDir,
			Detail:     "Projekt-Dev-Container Compose-Service " + service,
		}, nil
	}
	if image != "" {
		return containerSourcePlan{Kind: "project-compose", Image: image, Detail: "Projekt-Dev-Container Compose-Image " + image}, nil
	}
	return containerSourcePlan{}, errors.New("Dev-Container-Compose-Service enthält weder image noch dockerfile")
}

func projectImageTag(def devContainerConfig) string {
	hash := def.Hash
	if len(hash) > 12 {
		hash = hash[:12]
	}
	if hash == "" {
		hash = "local"
	}
	return "shipyard-project:" + hash
}

func parseComposeService(text, service string) (image, contextDir, dockerfile string, found bool, err error) {
	if strings.Contains(text, "<<:") || strings.Contains(text, "<< :") {
		return "", "", "", false, errors.New("Dev-Container-Compose verwendet YAML-Merges und kann nicht sicher geprüft werden")
	}
	inServices := false
	servicesIndent := -1
	inService := false
	serviceIndent := -1
	bodyIndent := -1
	inBuild := false
	buildIndent := -1
	for _, line := range strings.Split(text, "\n") {
		raw, _ := splitYAMLComment(line)
		if strings.TrimSpace(raw) == "" {
			continue
		}
		indent := leadingIndent(raw)
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "-") {
			continue
		}
		key, value, hasValue := splitYAMLKey(trimmed)
		if !inServices {
			if key == "services" && !hasValue {
				inServices = true
				servicesIndent = indent
			}
			continue
		}
		if indent <= servicesIndent {
			break
		}
		if !inService {
			if key == service && !hasValue && indent > servicesIndent {
				inService = true
				serviceIndent = indent
				found = true
			}
			continue
		}
		if indent <= serviceIndent {
			break
		}
		if bodyIndent < 0 {
			bodyIndent = indent
		}
		if inBuild && indent <= buildIndent {
			inBuild = false
		}
		if inBuild && (key == "context" || key == "dockerfile") && hasValue {
			if key == "context" {
				contextDir = unquoteYAML(value)
			} else {
				dockerfile = unquoteYAML(value)
			}
			continue
		}
		if indent != bodyIndent {
			continue
		}
		switch key {
		case "image":
			if hasValue {
				image = unquoteYAML(value)
			}
		case "build":
			if hasValue && value != "" && value != "|" && value != ">" {
				dockerfile = unquoteYAML(value)
			} else {
				inBuild = true
				buildIndent = indent
			}
		}
	}
	return image, contextDir, dockerfile, found, nil
}

func splitYAMLComment(line string) (string, bool) {
	inSingle, inDouble := false, false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case '#':
			if !inSingle && !inDouble {
				return line[:i], true
			}
		}
	}
	return line, false
}

func leadingIndent(line string) int {
	indent := 0
	for _, r := range line {
		switch r {
		case ' ':
			indent++
		case '\t':
			indent += 2
		default:
			return indent
		}
	}
	return indent
}

func splitYAMLKey(trimmed string) (key, value string, hasValue bool) {
	key, value, found := strings.Cut(trimmed, ":")
	key = strings.TrimSpace(key)
	if !found {
		return key, "", false
	}
	value = strings.TrimSpace(value)
	return key, value, value != ""
}

func unquoteYAML(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}

func agentContainerMounts(worktree string, policy sandbox.Profile, homeHost string, auth hostCLIAuthPlan, cliBinds, extraWritable, hostMounts []string, relaySocket, relayScript string) ([]container.Mount, error) {
	worktree = filepath.Clean(worktree)
	var mounts []container.Mount
	seen := map[string]struct{}{}
	add := func(source, target string, readOnly bool) error {
		source = filepath.Clean(source)
		target = filepath.Clean(target)
		if source == "" || source == "/" || !filepath.IsAbs(source) {
			return errors.New("container bind source must be an absolute non-root path")
		}
		if strings.Contains(source, "docker.sock") || strings.Contains(target, "docker.sock") {
			return errors.New("docker socket mounts are not allowed")
		}
		mounts = append(mounts, container.Mount{Source: source, Target: target, ReadOnly: readOnly})
		return nil
	}
	addOnce := func(source, target string, readOnly bool) error {
		source = filepath.Clean(source)
		if _, ok := seen[source+"\x00"+target]; ok {
			return nil
		}
		seen[source+"\x00"+target] = struct{}{}
		return add(source, target, readOnly)
	}
	for _, hostPath := range hostMounts {
		hostPath = filepath.Clean(hostPath)
		if hostPath == worktree || pathWithin(worktree, hostPath) {
			continue
		}
		if err := addOnce(hostPath, hostPath, true); err != nil {
			return nil, err
		}
	}
	for _, bind := range cliBinds {
		bindAbs, err := filepath.Abs(bind)
		if err != nil {
			return nil, err
		}
		bindAbs = filepath.Clean(bindAbs)
		if bindAbs == worktree || pathWithin(worktree, bindAbs) {
			continue
		}
		if err := addOnce(bindAbs, bindAbs, true); err != nil {
			return nil, err
		}
	}
	if err := addOnce(worktree, worktree, policy.WriteMode == "readonly"); err != nil {
		return nil, err
	}
	for _, writable := range extraWritable {
		writableAbs, err := filepath.Abs(writable)
		if err != nil {
			return nil, err
		}
		writableAbs = filepath.Clean(writableAbs)
		if writableAbs == "/" || writableAbs == worktree || pathWithin(worktree, writableAbs) || pathWithin(writableAbs, worktree) {
			continue
		}
		info, statErr := os.Stat(writableAbs)
		if statErr != nil || !info.IsDir() {
			continue
		}
		if err := addOnce(writableAbs, writableAbs, false); err != nil {
			return nil, err
		}
	}
	if err := addOnce(homeHost, containerAgentHome, false); err != nil {
		return nil, err
	}
	for _, bind := range auth.Binds {
		if !safeHostAuthSource(bind.Source, worktree) {
			continue
		}
		if err := addOnce(bind.Source, bind.Dest, true); err != nil {
			return nil, err
		}
	}
	if relaySocket != "" {
		if err := addOnce(relaySocket, "/tmp/shipyard-model-api.sock", false); err != nil {
			return nil, err
		}
	}
	if relayScript != "" {
		if err := addOnce(relayScript, "/tmp/shipyard-model-api-relay.py", true); err != nil {
			return nil, err
		}
	}
	return mounts, nil
}

func containerCreateEnv(auth hostCLIAuthPlan) []container.Env {
	entries := []container.Env{
		{Key: "HOME", Value: containerAgentHome},
		{Key: "PATH", Value: containerDefaultPATH},
		{Key: "LANG", Value: "C.UTF-8"},
		{Key: "LC_ALL", Value: "C.UTF-8"},
		{Key: "XDG_CONFIG_HOME", Value: filepath.Join(containerAgentHome, ".config")},
		{Key: "XDG_CACHE_HOME", Value: filepath.Join(containerAgentHome, ".cache")},
		{Key: "GOTOOLCHAIN", Value: "local"},
		{Key: "NO_COLOR", Value: "1"},
		{Key: "TERM", Value: "dumb"},
	}
	for _, env := range auth.Env {
		entries = append(entries, container.Env{Key: env.Key, Value: env.Value})
	}
	return entries
}

const containerDefaultPATH = "/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

func containerExecEnv(auth hostCLIAuthPlan, secrets []domain.SecretValue, hostEnv []sandboxEnvVar, extraPath string, relay bool) []container.Env {
	path := containerDefaultPATH
	if extraPath != "" {
		path = extraPath + ":" + path
	}
	entries := containerCreateEnv(auth)
	for i := range entries {
		if entries[i].Key == "PATH" {
			entries[i].Value = path
		}
	}
	for _, key := range []string{"TASKBOARD_MCP_TOKEN", "SHIPYARD_TEST_DATABASE_URL"} {
		if value, ok := os.LookupEnv(key); ok && value != "" {
			entries = append(entries, container.Env{Key: key, Value: value})
		}
	}
	for _, env := range hostEnv {
		entries = append(entries, container.Env{Key: env.Key, Value: env.Value})
	}
	if relay {
		entries = append(entries, container.Env{Key: "SHIPYARD_MODEL_API_SOCKET", Value: "/tmp/shipyard-model-api.sock"})
	}
	for _, secret := range secrets {
		entries = append(entries, container.Env{Key: secret.EnvName, Value: secret.Value})
	}
	return entries
}

func writeContainerEnvFile(path string, entries []container.Env) error {
	var b strings.Builder
	for _, entry := range entries {
		if entry.Key == "" || strings.ContainsAny(entry.Key, "=\n\r") || strings.ContainsAny(entry.Value, "\n\r") {
			return errors.New("container environment entry is invalid")
		}
		b.WriteString(entry.Key)
		b.WriteByte('=')
		b.WriteString(entry.Value)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
