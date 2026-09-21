package automation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"taskboard/internal/container"
	"taskboard/internal/domain"
	"taskboard/internal/sandbox"
	"taskboard/internal/workspace"
)

const containerAgentHome = sandboxCLIHome

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
//   - Task-assigned secrets are injected through an exec env-file. The
//     container does not receive the Shipyard service environment.
//   - Bind sources Shipyard creates (agent home, secret env-file, model-API
//     socket, relay script, fallback build context) live under the workspace
//     runtime directory. /tmp and /var/tmp are private to the systemd unit
//     (PrivateTmp=true) and are invisible to rootless dockerd. The agent home
//     is created again immediately before the runtime create call.
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
		buildDir, mkErr := os.MkdirTemp(bindRoot, "fallback-build-")
		if mkErr != nil {
			return nil, mkErr
		}
		session.closeFns = append(session.closeFns, func() error { return os.RemoveAll(buildDir) })
		if err := requireDurableBind(buildDir); err != nil {
			return nil, err
		}
		if err := container.MaterializeFallback(buildDir); err != nil {
			return nil, err
		}
		plan.Dockerfile = filepath.Join(buildDir, "Dockerfile")
		plan.Context = buildDir
		plan.Image = container.FallbackImage
	}
	resolved, cliBinds, extraPath, err := resolveCLICommand(req.Command)
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
		if err := relay.Chmod(0o500); err != nil {
			_ = relay.Close()
			return nil, err
		}
		if err := relay.Close(); err != nil {
			return nil, err
		}
		relaySocket = proxy.path
		if err := requireDurableBind(relayScript); err != nil {
			return nil, err
		}
		if err := requireDurableBind(relaySocket); err != nil {
			return nil, err
		}
		session.ModelAPIProxy = true
	}
	user := containerUser()
	mounts, err := agentContainerMounts(worktree, req.Policy, homeHost, auth, cliBinds, req.ExtraWritable, hostMounts, relaySocket, relayScript)
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
			if _, err := os.Stat(relaySocket); err != nil {
				return fmt.Errorf("Modell-API-Proxy-Socket fehlt vor dem Container-Start: %w", err)
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
	envFile := homeHost + ".env"
	if err := requireDurableBind(envFile); err != nil {
		return nil, err
	}
	entries := containerExecEnv(auth, req.Secrets, hostEnv, extraPath, relaySocket != "")
	if err := writeContainerEnvFile(envFile, entries); err != nil {
		return nil, err
	}
	session.closeFns = append(session.closeFns, func() error { return os.Remove(envFile) })
	execCommand := []string{resolved}
	execCommand = append(execCommand, req.Args...)
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
	if err := os.Chmod(homeHost, 0o700); err != nil {
		return err
	}
	for _, dir := range auth.DestDirs {
		rel, relErr := filepath.Rel(containerAgentHome, dir)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errors.New("Host-CLI-Login liegt außerhalb des Container-Home")
		}
		if err := os.MkdirAll(filepath.Join(homeHost, rel), 0o700); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(homeHost, ".config"), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(homeHost, ".cache"), 0o700); err != nil {
		return err
	}
	return nil
}

func ensureRelayScript(path string) error {
	if _, err := os.Stat(path); err == nil {
		return os.Chmod(path, 0o500)
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.WriteFile(path, modelAPIRelayScript, 0o500); err != nil {
		return err
	}
	return os.Chmod(path, 0o500)
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

func planContainerSource(worktree string, def devContainerConfig, has bool) (containerSourcePlan, error) {
	if !has {
		return containerSourcePlan{Kind: "fallback", Image: container.FallbackImage, Detail: "generischer Fallback (" + container.FallbackImage + ")"}, nil
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
