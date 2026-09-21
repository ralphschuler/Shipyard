package automation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"taskboard/internal/domain"
	"taskboard/internal/sandbox"

	_ "embed"
)

//go:embed sandbox_relay.py
var modelAPIRelayScript []byte

const (
	sandboxLayoutTool sandboxLayout = iota
	sandboxLayoutCLI
)

type sandboxLayout int

type sandboxExec struct {
	Worktree      string
	Policy        sandbox.Profile
	Layout        sandboxLayout
	Provider      string
	Command       string
	Args          []string
	ShellCommand  string
	ExtraWritable []string
	ExtraROBinds  []string
	ExtraPATH     string
	RelayScript   string
	RelaySocket   string
	RelayPython   string
	HostCLIAuth   *hostCLIAuthPlan
}

type cliSandboxRequest struct {
	Worktree      string
	Policy        sandbox.Profile
	Command       string
	Args          []string
	ExtraWritable []string
	Provider      domain.ProviderSetting
}

type cliSandbox struct {
	Command         string
	Args            []string
	ModelAPIProxy   bool
	IsolationLog    string
	HostAuthLog     string
	HostAuthMounted []string
	closeFns        []func() error
}

func (s *cliSandbox) Close() error {
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

func sandboxIsolationSummary(policy sandbox.Profile) string {
	return fmt.Sprintf("Sandbox-Profil wirksam: %s · Netzwerk=%s · Schreiben=%s · Isolation=bwrap", policy.Name, policy.NetworkMode, policy.WriteMode)
}

func sandboxSystemROBindArgs() []string {
	var args []string
	for _, directory := range []string{"/usr", "/bin", "/lib", "/lib64"} {
		if _, err := os.Stat(directory); err == nil {
			args = append(args, "--ro-bind", directory, directory)
		}
	}
	return args
}

func sandboxTLSROBindArgs() []string {
	var args []string
	for _, directory := range []string{"/etc/ssl", "/etc/ca-certificates", "/etc/pki"} {
		if info, err := os.Stat(directory); err == nil && info.IsDir() {
			args = append(args, "--ro-bind", directory, directory)
		}
	}
	return args
}

func sandboxQANetROBindArgs() []string {
	var args []string
	for _, file := range []string{"/etc/resolv.conf", "/etc/hosts"} {
		if _, err := os.Stat(file); err == nil {
			args = append(args, "--ro-bind", file, file)
		}
	}
	return args
}

func sandboxWorktreeBind(abs string, policy sandbox.Profile, dest string) []string {
	bind := "--bind"
	if policy.WriteMode == "readonly" {
		bind = "--ro-bind"
	}
	return []string{bind, abs, dest, "--chdir", dest}
}

// buildSandboxArgs turns a frozen sandbox profile into bubblewrap arguments.
// The profile model is unchanged: network mode is not widened, and the only
// writable mount is the run worktree (plus existing caller extras). CLI
// sandboxes additionally ro-bind host paths the worktree mount does not cover:
//   - the Go module cache (GOMODCACHE, else ~/go/pkg/mod) when that directory
//     already exists, with GOMODCACHE and GOTOOLCHAIN set so go test stays offline
//   - the worktree git common dir, and the project checkout when a relative
//     gitdir must walk through it, so gitdir pointers outside the run bind resolve
func buildSandboxArgs(spec sandboxExec) ([]string, error) {
	abs, err := filepath.Abs(spec.Worktree)
	if err != nil {
		return nil, err
	}
	if _, err := sandbox.EffectiveProfile(spec.Policy, abs); err != nil {
		return nil, err
	}
	switch spec.Policy.NetworkMode {
	case "none", "qa-network":
	case "bridge-only":
		return nil, errors.New("release-bridge erlaubt nur den hostseitigen Release-Bridge-Dienst")
	default:
		return nil, fmt.Errorf("Sandbox-Netzwerkmodus %q wird vom Ausführungsadapter nicht unterstützt", spec.Policy.NetworkMode)
	}
	if spec.Policy.WriteMode != "worktree" && spec.Policy.WriteMode != "readonly" {
		return nil, fmt.Errorf("Sandbox-Schreibmodus %q wird vom Ausführungsadapter nicht unterstützt", spec.Policy.WriteMode)
	}
	args := []string{"--die-with-parent", "--unshare-all", "--new-session"}
	if spec.Policy.NetworkMode == "qa-network" {
		args = append(args, "--share-net")
	}
	args = append(args, sandboxSystemROBindArgs()...)
	if spec.Layout == sandboxLayoutCLI || spec.Policy.NetworkMode == "qa-network" {
		args = append(args, sandboxTLSROBindArgs()...)
	}
	if spec.Policy.NetworkMode == "qa-network" {
		args = append(args, sandboxQANetROBindArgs()...)
	}
	args = append(args, "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp")
	dest := "/workspace"
	home := "/workspace"
	path := "/usr/bin:/bin"
	auth := hostCLIAuthPlan{}
	var cliEnv []sandboxEnvVar
	if spec.Layout == sandboxLayoutCLI {
		dest = abs
		home = sandboxCLIHome
		path = "/usr/bin:/bin:/usr/local/bin"
		if spec.ExtraPATH != "" {
			path = spec.ExtraPATH + ":" + path
		}
		args = append(args, "--tmpfs", home)
		if spec.HostCLIAuth != nil {
			auth = *spec.HostCLIAuth
		} else {
			auth = resolveHostCLIAuth(spec.Provider, abs)
		}
		args = auth.appendDirArgs(args)
		// Ancestor checkouts are mounted before the worktree so a read-only
		// parent cannot hide the run directory, which stays the writable root.
		var hostMounts []string
		hostMounts, cliEnv = cliSandboxHostMounts(abs)
		for _, hostPath := range hostMounts {
			args = append(args, "--ro-bind", hostPath, hostPath)
		}
	}
	args = append(args, sandboxWorktreeBind(abs, spec.Policy, dest)...)
	for _, extra := range spec.ExtraROBinds {
		extraAbs, extraErr := filepath.Abs(extra)
		if extraErr != nil {
			return nil, extraErr
		}
		if extraAbs == "/" || extraAbs == abs {
			continue
		}
		args = append(args, "--ro-bind", extraAbs, extraAbs)
	}
	for _, extra := range spec.ExtraWritable {
		extraAbs, extraErr := filepath.Abs(extra)
		if extraErr != nil {
			return nil, extraErr
		}
		if extraAbs == "/" || extraAbs == abs || strings.HasPrefix(extraAbs+string(filepath.Separator), abs+string(filepath.Separator)) {
			continue
		}
		if !filepath.IsAbs(extraAbs) {
			return nil, errors.New("zusätzlicher Sandbox-Schreibpfad muss absolut sein")
		}
		args = append(args, "--bind", extraAbs, extraAbs)
	}
	if spec.RelaySocket != "" {
		args = append(args, "--bind", spec.RelaySocket, "/tmp/shipyard-model-api.sock")
	}
	if spec.RelayScript != "" {
		args = append(args, "--ro-bind", spec.RelayScript, "/tmp/shipyard-model-api-relay.py")
	}
	args = auth.appendBindArgs(args)
	args = append(args,
		"--setenv", "HOME", home,
		"--setenv", "PATH", path,
		"--setenv", "LANG", "C",
		"--setenv", "XDG_CONFIG_HOME", filepath.Join(home, ".config"),
		"--setenv", "XDG_CACHE_HOME", filepath.Join(home, ".cache"),
	)
	args = auth.appendEnvArgs(args)
	for _, env := range cliEnv {
		args = append(args, "--setenv", env.Key, env.Value)
	}
	if spec.RelaySocket != "" {
		args = append(args, "--setenv", "SHIPYARD_MODEL_API_SOCKET", "/tmp/shipyard-model-api.sock")
	}
	if spec.Layout == sandboxLayoutTool {
		if strings.TrimSpace(spec.ShellCommand) == "" {
			return nil, errors.New("sandbox command is empty")
		}
		return append(args, "/bin/sh", "-lc", spec.ShellCommand), nil
	}
	if strings.TrimSpace(spec.Command) == "" {
		return nil, errors.New("CLI-Kommando fehlt")
	}
	if spec.RelayScript != "" {
		python := spec.RelayPython
		if python == "" {
			var err error
			python, err = exec.LookPath("python3")
			if err != nil {
				return nil, errors.New("CLI-Sandbox kann Modell-API-Zugang nicht vom Tool-Netzwerk trennen (python3 fehlt)")
			}
		}
		return append(append(args, python, "/tmp/shipyard-model-api-relay.py", spec.Command), spec.Args...), nil
	}
	return append(append(args, spec.Command), spec.Args...), nil
}

func openAISandboxArgs(worktree, command string) ([]string, error) {
	return openAISandboxArgsForProfile(worktree, command, "strict")
}

func openAISandboxArgsForProfile(worktree, command, profile string) ([]string, error) {
	abs, err := filepath.Abs(worktree)
	if err != nil {
		return nil, err
	}
	policy, err := sandbox.Effective(profile, abs)
	if err != nil || policy.NetworkMode == "bridge-only" {
		return nil, errors.New("sandbox profile does not permit direct provider commands")
	}
	return openAISandboxArgsForPolicy(abs, command, policy)
}

func openAISandboxArgsForPolicy(abs, command string, policy sandbox.Profile) ([]string, error) {
	if _, err := sandbox.EffectiveProfile(policy, abs); err != nil || policy.NetworkMode == "bridge-only" {
		return nil, errors.New("sandbox profile does not permit direct provider commands")
	}
	return buildSandboxArgs(sandboxExec{
		Worktree:     abs,
		Policy:       policy,
		Layout:       sandboxLayoutTool,
		ShellCommand: command,
	})
}

// cliSandboxInvocation builds the bubblewrap envelope for local CLI adapters.
// Writes stay on the run worktree; read-only module-cache and git metadata
// binds are added by buildSandboxArgs. The caller still controls provider args.
func cliSandboxInvocation(worktree, profileName, command string, commandArgs []string) (string, []string, error) {
	profile, err := sandbox.Effective(profileName, worktree)
	if err != nil {
		return "", nil, err
	}
	args, err := cliSandboxArgs(worktree, profile, command, commandArgs, nil)
	if err != nil {
		return "", nil, err
	}
	return "bwrap", args, nil
}

func cliSandboxArgs(worktree string, policy sandbox.Profile, command string, commandArgs, extraWritable []string) ([]string, error) {
	return buildSandboxArgs(sandboxExec{
		Worktree:      worktree,
		Policy:        policy,
		Layout:        sandboxLayoutCLI,
		Command:       command,
		Args:          commandArgs,
		ExtraWritable: extraWritable,
	})
}

// startCLISandbox is the production CLI execution boundary. It derives
// Bubblewrap arguments from the frozen run profile, fails closed when the
// combination cannot be enforced, and (for NetworkMode=none) starts a host
// allowlist proxy so model-API traffic is distinct from tool/project network.
func startCLISandbox(ctx context.Context, req cliSandboxRequest) (*cliSandbox, error) {
	if _, err := sandbox.EffectiveProfile(req.Policy, req.Worktree); err != nil {
		return nil, err
	}
	if req.Policy.NetworkMode == "bridge-only" {
		return nil, errors.New("release-bridge erlaubt nur den hostseitigen Release-Bridge-Dienst")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		return nil, errors.New("CLI-Agenten benötigen bubblewrap (bwrap) für das Sandbox-Profil; installiere das Paket bubblewrap auf dem Server und starte taskboard.service neu")
	}
	if os.Getenv("TASKBOARD_BWRAP_PREFLIGHT") != "0" {
		if err := bubblewrapPreflight(ctx); err != nil {
			return nil, err
		}
	}
	command, extraBinds, extraPath, err := resolveCLICommand(req.Command)
	if err != nil {
		return nil, err
	}
	auth := resolveHostCLIAuth(req.Provider.Provider, req.Worktree)
	session := &cliSandbox{
		Command:         "bwrap",
		IsolationLog:    sandboxIsolationSummary(req.Policy),
		HostAuthLog:     auth.infoLog(),
		HostAuthMounted: append([]string(nil), auth.Names...),
	}
	spec := sandboxExec{
		Worktree:      req.Worktree,
		Policy:        req.Policy,
		Layout:        sandboxLayoutCLI,
		Provider:      req.Provider.Provider,
		Command:       command,
		Args:          req.Args,
		ExtraWritable: req.ExtraWritable,
		ExtraROBinds:  extraBinds,
		ExtraPATH:     extraPath,
		HostCLIAuth:   &auth,
	}
	if req.Policy.NetworkMode == "none" {
		python, err := exec.LookPath("python3")
		if err != nil {
			return nil, errors.New("CLI-Sandbox kann Modell-API-Zugang nicht vom Tool-Netzwerk trennen (python3 fehlt)")
		}
		python, extraPython, extraPythonPath, err := resolveCLICommand(python)
		if err != nil {
			return nil, err
		}
		spec.RelayPython = python
		spec.ExtraROBinds = uniquePaths(append(append([]string{}, spec.ExtraROBinds...), extraPython...))
		if extraPythonPath != "" && spec.ExtraPATH == "" {
			spec.ExtraPATH = extraPythonPath
		} else if extraPythonPath != "" && !strings.Contains(spec.ExtraPATH, extraPythonPath) {
			spec.ExtraPATH = extraPythonPath + ":" + spec.ExtraPATH
		}
		proxy, err := startModelAPIProxy(modelAPIAllowlist(req.Provider))
		if err != nil {
			return nil, fmt.Errorf("Modell-API-Proxy konnte nicht gestartet werden: %w", err)
		}
		session.closeFns = append(session.closeFns, proxy.Close)
		relay, err := os.CreateTemp("", "shipyard-model-api-relay-*.py")
		if err != nil {
			_ = session.Close()
			return nil, err
		}
		if _, err := relay.Write(modelAPIRelayScript); err != nil {
			_ = relay.Close()
			_ = os.Remove(relay.Name())
			_ = session.Close()
			return nil, err
		}
		if err := relay.Chmod(0o500); err != nil {
			_ = relay.Close()
			_ = os.Remove(relay.Name())
			_ = session.Close()
			return nil, err
		}
		if err := relay.Close(); err != nil {
			_ = os.Remove(relay.Name())
			_ = session.Close()
			return nil, err
		}
		relayPath := relay.Name()
		session.closeFns = append(session.closeFns, func() error { return os.Remove(relayPath) })
		spec.RelaySocket = proxy.path
		spec.RelayScript = relayPath
		session.ModelAPIProxy = true
	}
	args, err := buildSandboxArgs(spec)
	if err != nil {
		_ = session.Close()
		return nil, err
	}
	session.Args = args
	return session, nil
}

func resolveCLICommand(command string) (string, []string, string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", nil, "", errors.New("CLI-Kommando fehlt")
	}
	resolved, err := exec.LookPath(command)
	if err != nil {
		if filepath.IsAbs(command) {
			return "", nil, "", fmt.Errorf("CLI-Executable %q nicht gefunden", command)
		}
		return command, nil, "", nil
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", nil, "", err
	}
	real := resolved
	if target, evalErr := filepath.EvalSymlinks(resolved); evalErr == nil && target != "" {
		real = target
	}
	if sandboxSystemPath(real) && sandboxSystemPath(resolved) {
		return resolved, nil, "", nil
	}
	binds := []string{real}
	if resolved != real {
		binds = append(binds, resolved)
	}
	extraPath := ""
	if dir := filepath.Dir(real); dir != "/" && !sandboxSystemPath(dir) {
		binds = append(binds, dir)
		extraPath = dir
	}
	return resolved, uniquePaths(binds), extraPath, nil
}

func sandboxSystemPath(path string) bool {
	path = filepath.Clean(path)
	for _, root := range []string{"/usr", "/bin", "/lib", "/lib64"} {
		if path == root || strings.HasPrefix(path, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func uniquePaths(paths []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.Clean(path)
		if path == "" || path == "/" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		result = append(result, path)
	}
	return result
}

// cliSandboxHostMounts returns read-only host directories the CLI sandbox must
// see at the same absolute path, plus the Go env that makes those binds useful.
// Nothing here is writable. Missing caches and git metadata are skipped and
// never created.
func cliSandboxHostMounts(worktree string) ([]string, []sandboxEnvVar) {
	worktree = filepath.Clean(worktree)
	seen := map[string]struct{}{}
	var binds []string
	add := func(path string, allowAncestor bool) {
		path = filepath.Clean(path)
		if !sandboxROBindAllowed(path, worktree, allowAncestor) {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		binds = append(binds, path)
	}

	var env []sandboxEnvVar
	if cache, ok := goModuleCacheDir(); ok && !tooBroadSandboxBind(cache) {
		visible := pathWithin(worktree, cache)
		if sandboxROBindAllowed(cache, worktree, false) {
			add(cache, false)
			visible = true
		}
		if visible {
			env = append(env,
				sandboxEnvVar{Key: "GOMODCACHE", Value: cache},
				sandboxEnvVar{Key: "GOTOOLCHAIN", Value: sandboxGoToolchain()},
			)
		}
	}
	gitBinds, ancestorBinds := gitSandboxROBinds(worktree)
	for _, path := range gitBinds {
		add(path, false)
	}
	for _, path := range ancestorBinds {
		add(path, true)
	}
	sort.Slice(binds, func(i, j int) bool {
		if len(binds[i]) != len(binds[j]) {
			return len(binds[i]) < len(binds[j])
		}
		return binds[i] < binds[j]
	})
	return binds, env
}

func sandboxGoToolchain() string {
	if value := strings.TrimSpace(os.Getenv("GOTOOLCHAIN")); value != "" {
		return value
	}
	return "local"
}

// goModuleCacheDir prefers GOMODCACHE when it names an existing directory.
// An empty or unset value falls back to the service user's ~/go/pkg/mod.
// A set-but-missing GOMODCACHE does not fall back and is not created.
func goModuleCacheDir() (string, bool) {
	if raw, ok := os.LookupEnv("GOMODCACHE"); ok && strings.TrimSpace(raw) != "" {
		return existingDirectory(raw)
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return "", false
	}
	return existingDirectory(filepath.Join(home, "go", "pkg", "mod"))
}

func existingDirectory(path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	abs = filepath.Clean(abs)
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return abs, true
}

// gitSandboxROBinds resolves the worktree git common dir by parsing the
// .git gitdir pointer and walking commondir up to the real .git directory.
// Binding that common dir covers .git/worktrees/<id>. The project checkout
// (parent of .git) is included only when a relative gitdir has to walk
// through it. Paths already inside the run worktree are omitted so a
// writable worktree .git is not remounted read-only.
func gitSandboxROBinds(worktree string) (binds, ancestorBinds []string) {
	gitDir, commonDir, relativeEscape, ok := resolveWorktreeGitDirs(worktree)
	if !ok {
		return nil, nil
	}
	commonMounted := false
	if common, found := existingDirectory(commonDir); found && looksLikeGitDir(common) {
		binds = append(binds, common)
		commonMounted = true
	}
	// The worktree gitdir lives under the common dir in the usual
	// .git/worktrees/<id> layout, so the common-dir bind is enough.
	if git, found := existingDirectory(gitDir); found && looksLikeGitDir(git) && !(commonMounted && pathWithin(commonDir, git)) {
		binds = append(binds, git)
	}
	if relativeEscape && commonMounted {
		if project := projectCheckout(commonDir); project != "" {
			if root, found := existingDirectory(project); found {
				ancestorBinds = append(ancestorBinds, root)
			}
		}
	}
	return binds, ancestorBinds
}

func looksLikeGitDir(path string) bool {
	info, err := os.Stat(filepath.Join(path, "HEAD"))
	return err == nil && info.Mode().IsRegular()
}

func resolveWorktreeGitDirs(worktree string) (gitDir, commonDir string, relativeEscape, ok bool) {
	dotGit := filepath.Join(worktree, ".git")
	info, err := os.Lstat(dotGit)
	if err != nil {
		return "", "", false, false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if target, evalErr := filepath.EvalSymlinks(dotGit); evalErr == nil && target != "" {
			target = filepath.Clean(target)
			if st, statErr := os.Stat(target); statErr == nil && st.IsDir() && looksLikeGitDir(target) {
				return target, commonDirFromGitDir(target), false, true
			}
		}
		info, err = os.Stat(dotGit)
		if err != nil {
			return "", "", false, false
		}
	}
	if info.IsDir() {
		return dotGit, dotGit, false, true
	}
	if !info.Mode().IsRegular() {
		return "", "", false, false
	}
	raw, found := parseGitdirPointer(dotGit)
	if !found {
		return "", "", false, false
	}
	relativeEscape = relativePathEscapes(raw)
	if filepath.IsAbs(raw) {
		gitDir = filepath.Clean(raw)
	} else {
		gitDir = filepath.Clean(filepath.Join(worktree, raw))
	}
	if _, found := existingDirectory(gitDir); !found {
		return "", "", false, false
	}
	return gitDir, commonDirFromGitDir(gitDir), relativeEscape, true
}

func parseGitdirPointer(dotGitPath string) (string, bool) {
	data, err := os.ReadFile(dotGitPath)
	if err != nil {
		return "", false
	}
	line := string(data)
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	const prefix = "gitdir:"
	if len(line) < len(prefix) || !strings.EqualFold(line[:len(prefix)], prefix) {
		return "", false
	}
	raw := strings.TrimSpace(line[len(prefix):])
	if raw == "" {
		return "", false
	}
	return raw, true
}

func commonDirFromGitDir(gitDir string) string {
	data, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err == nil {
		rel := strings.TrimSpace(string(data))
		if rel != "" {
			if filepath.IsAbs(rel) {
				return filepath.Clean(rel)
			}
			return filepath.Clean(filepath.Join(gitDir, rel))
		}
	}
	dir := filepath.Clean(gitDir)
	// .git/worktrees/<id> → the real .git directory two levels up.
	if filepath.Base(filepath.Dir(dir)) == "worktrees" {
		parent := filepath.Dir(filepath.Dir(dir))
		if parent != "" && parent != string(filepath.Separator) {
			return parent
		}
	}
	return dir
}

func projectCheckout(commonDir string) string {
	commonDir = filepath.Clean(commonDir)
	if filepath.Base(commonDir) != ".git" {
		return ""
	}
	project := filepath.Dir(commonDir)
	if project == "" || project == "." || project == string(filepath.Separator) || tooBroadSandboxBind(project) {
		return ""
	}
	return project
}

func relativePathEscapes(raw string) bool {
	if filepath.IsAbs(raw) {
		return false
	}
	cleaned := filepath.Clean(raw)
	return cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator))
}

func sandboxROBindAllowed(path, worktree string, allowAncestor bool) bool {
	path = filepath.Clean(path)
	worktree = filepath.Clean(worktree)
	if path == "" || !filepath.IsAbs(path) || tooBroadSandboxBind(path) {
		return false
	}
	if path == worktree || pathWithin(worktree, path) {
		return false
	}
	if !allowAncestor && pathWithin(path, worktree) {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func tooBroadSandboxBind(path string) bool {
	switch filepath.Clean(path) {
	case "/", "/home", "/root", "/usr", "/bin", "/lib", "/lib64", "/etc", "/tmp", "/var", "/opt", "/proc", "/dev", "/sys", "/run", "/boot", "/mnt", "/media":
		return true
	default:
		return false
	}
}
