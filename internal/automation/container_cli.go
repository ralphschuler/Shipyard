package automation

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// containerCLI is how a host provider command runs inside an agent container.
// Argv is the process the container execs. Binds are host paths mounted
// read-only at the same absolute path. NeedsNode is set when Argv starts
// with the image's node, which the agent base already provides.
type containerCLI struct {
	Argv      []string
	Binds     []string
	ExtraPATH string
	NeedsNode bool
}

// Image binaries the agent base and Dev Container already ship. Host copies
// of these stay off the mount list so a host toolchain cannot hide the image.
var containerImageTools = map[string]struct{}{
	"sh": {}, "bash": {}, "dash": {},
	"git":     {},
	"python3": {}, "python": {},
	"node": {}, "npm": {}, "npx": {}, "corepack": {},
	"go": {}, "gofmt": {},
	"curl": {},
}

// resolveContainerCLI makes a host CLI visible inside the agent image.
// Bubblewrap can see host /usr because it bind-mounts that tree. The agent
// image has its own /usr and does not contain host installs such as
// /usr/local/bin/codex. A Node entrypoint is executed with the image node,
// and its package directory is mounted at the real host path so the launcher
// can find its platform binary. Login directories are not part of this mount.
func resolveContainerCLI(command string) (containerCLI, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return containerCLI{}, errors.New("CLI-Kommando fehlt")
	}
	resolved, err := exec.LookPath(command)
	if err != nil {
		if filepath.IsAbs(command) {
			return containerCLI{}, fmt.Errorf("CLI-Executable %q nicht gefunden", command)
		}
		return containerCLI{Argv: []string{command}}, nil
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return containerCLI{}, err
	}
	real := resolved
	if target, evalErr := filepath.EvalSymlinks(resolved); evalErr == nil && target != "" {
		real = target
	} else if evalErr != nil {
		info, statErr := os.Lstat(resolved)
		if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return containerCLI{}, fmt.Errorf("CLI-Executable %q kann nicht aufgelöst werden: %w", resolved, evalErr)
		}
	}
	real = filepath.Clean(real)
	if containerImageProvides(resolved, real) {
		return containerCLI{Argv: []string{resolved}}, nil
	}
	if nodeScript(real) {
		binds, err := nodePackageBinds(real)
		if err != nil {
			return containerCLI{}, err
		}
		return containerCLI{
			Argv:      []string{"node", real},
			Binds:     binds,
			NeedsNode: true,
		}, nil
	}
	root := nativeCLIMountRoot(real)
	if !mountRootAllowed(root) {
		return containerCLI{}, fmt.Errorf("Host-CLI %q kann nicht in den Container eingehängt werden", real)
	}
	binds := mountClosure([]string{root})
	if len(binds) == 0 {
		return containerCLI{}, fmt.Errorf("Host-CLI %q kann nicht in den Container eingehängt werden", real)
	}
	extra := ""
	if bin := filepath.Dir(real); filepath.Base(bin) == "bin" && !sandboxSystemPath(bin) {
		extra = bin
	}
	return containerCLI{Argv: []string{real}, Binds: binds, ExtraPATH: extra}, nil
}

func containerImageProvides(resolved, real string) bool {
	if _, ok := containerImageTools[filepath.Base(real)]; !ok {
		return false
	}
	return sandboxSystemPath(resolved) && sandboxSystemPath(real)
}

func nodeScript(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".mjs", ".cjs":
		return true
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	buf := make([]byte, 160)
	n, _ := file.Read(buf)
	line := string(buf[:n])
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	if !strings.HasPrefix(line, "#!") {
		return false
	}
	return strings.Contains(line, "node")
}

func nodePackageBinds(script string) ([]string, error) {
	script = filepath.Clean(script)
	modules := nearestNodeModules(script)
	if modules == "" {
		root := nativeCLIMountRoot(script)
		if !mountRootAllowed(root) {
			return nil, fmt.Errorf("Host-CLI %q kann nicht in den Container eingehängt werden", script)
		}
		binds := mountClosure([]string{root})
		if len(binds) == 0 {
			return nil, fmt.Errorf("Host-CLI %q kann nicht in den Container eingehängt werden", script)
		}
		return binds, nil
	}
	rel, err := filepath.Rel(modules, script)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) == 0 || parts[0] == ".." || parts[0] == "" {
		return nil, fmt.Errorf("Host-CLI %q liegt außerhalb von node_modules", script)
	}
	var pkgDir string
	var roots []string
	if strings.HasPrefix(parts[0], "@") {
		if len(parts) < 2 || parts[1] == "" {
			return nil, fmt.Errorf("Host-CLI %q hat einen unvollständigen Paketpfad", script)
		}
		scope := filepath.Join(modules, parts[0])
		pkgDir = filepath.Join(scope, parts[1])
		if !mountRootAllowed(scope) {
			return nil, fmt.Errorf("Host-CLI %q kann nicht in den Container eingehängt werden", script)
		}
		roots = append(roots, scope)
	} else {
		pkgDir = filepath.Join(modules, parts[0])
		if !mountRootAllowed(pkgDir) {
			return nil, fmt.Errorf("Host-CLI %q kann nicht in den Container eingehängt werden", script)
		}
		roots = append(roots, pkgDir)
	}
	roots = append(roots, siblingDependencyMounts(modules, pkgDir, roots)...)
	binds := mountClosure(roots)
	if len(binds) == 0 {
		return nil, fmt.Errorf("Host-CLI %q kann nicht in den Container eingehängt werden", script)
	}
	return binds, nil
}

func nearestNodeModules(path string) string {
	path = filepath.Clean(path)
	dir := path
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		dir = filepath.Dir(path)
	}
	for {
		if filepath.Base(dir) == "node_modules" {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func siblingDependencyMounts(modules, pkgDir string, already []string) []string {
	seen := map[string]struct{}{}
	for _, root := range already {
		seen[filepath.Clean(root)] = struct{}{}
	}
	var extra []string
	queue := []string{pkgDir}
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		for _, name := range packageDependencyNames(filepath.Join(dir, "package.json")) {
			depDir, ok := installedPackageDir(modules, name)
			if !ok {
				continue
			}
			depDir = filepath.Clean(depDir)
			if _, ok := seen[depDir]; ok {
				continue
			}
			seen[depDir] = struct{}{}
			if pathCovered(append(already, extra...), depDir) || !mountRootAllowed(depDir) || pathInCLIConfigHome(depDir) {
				continue
			}
			extra = append(extra, depDir)
			queue = append(queue, depDir)
		}
	}
	return extra
}

func pathCovered(roots []string, path string) bool {
	for _, root := range roots {
		root = filepath.Clean(root)
		if path == root || pathWithin(root, path) {
			return true
		}
	}
	return false
}

type packageManifest struct {
	Dependencies         map[string]json.RawMessage `json:"dependencies"`
	OptionalDependencies map[string]json.RawMessage `json:"optionalDependencies"`
	PeerDependencies     map[string]json.RawMessage `json:"peerDependencies"`
}

func packageDependencyNames(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var manifest packageManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil
	}
	names := make([]string, 0, len(manifest.Dependencies)+len(manifest.OptionalDependencies)+len(manifest.PeerDependencies))
	for _, group := range []map[string]json.RawMessage{manifest.Dependencies, manifest.OptionalDependencies, manifest.PeerDependencies} {
		for name := range group {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func installedPackageDir(modules, name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || strings.Contains(name, "..") || filepath.IsAbs(name) || strings.Contains(name, "\\") {
		return "", false
	}
	parts := strings.Split(name, "/")
	switch {
	case strings.HasPrefix(name, "@"):
		if len(parts) != 2 || parts[0] == "@" || parts[1] == "" {
			return "", false
		}
	case len(parts) == 1:
	default:
		return "", false
	}
	dir := filepath.Join(append([]string{modules}, parts...)...)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return dir, true
}

func nativeCLIMountRoot(real string) string {
	real = filepath.Clean(real)
	dir := filepath.Dir(real)
	if filepath.Base(dir) == "bin" {
		root := filepath.Dir(dir)
		if mountRootAllowed(root) && !pathInCLIConfigHome(root) {
			return root
		}
		// A bin directory inside a CLI home holds the executable, not auth.json.
		if mountRootAllowed(dir) && !cliConfigHome(dir) {
			return dir
		}
	}
	return real
}

func mountRootAllowed(path string) bool {
	path = filepath.Clean(path)
	if path == "" || path == "/" || path == "." {
		return false
	}
	if filepath.Base(path) == "node_modules" {
		return false
	}
	if protectedContainerPath(path) || containerImageProvides(path, path) {
		return false
	}
	return true
}

func protectedContainerPath(path string) bool {
	path = filepath.Clean(path)
	switch path {
	case "/",
		"/usr", "/usr/bin", "/usr/lib", "/usr/lib64", "/usr/local", "/usr/local/bin", "/usr/local/lib", "/usr/local/sbin",
		"/bin", "/lib", "/lib64", "/sbin",
		"/usr/local/go", "/usr/local/go/bin",
		"/etc":
		return true
	default:
		return false
	}
}

func pathInCLIConfigHome(path string) bool {
	path = filepath.Clean(path)
	if cliConfigHome(path) {
		return true
	}
	parent := filepath.Dir(path)
	return parent != path && cliConfigHome(parent)
}

func cliConfigHome(path string) bool {
	switch filepath.Base(path) {
	case ".codex", ".grok", ".claude", ".config":
		return true
	}
	info, err := os.Stat(filepath.Join(path, "auth.json"))
	return err == nil && info.Mode().IsRegular()
}

// mountClosure adds directories a package symlink points at, so a relative
// link such as codex-linux-x64 -> cache still resolves after the package
// directory is mounted at the same absolute path.
func mountClosure(roots []string) []string {
	seen := map[string]struct{}{}
	var out []string
	add := func(path string, linked bool) {
		path = filepath.Clean(path)
		if path == "" || path == "/" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		if !mountRootAllowed(path) || cliConfigHome(path) {
			return
		}
		if linked && pathInCLIConfigHome(path) {
			return
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	for _, root := range roots {
		root = filepath.Clean(root)
		add(root, false)
		info, err := os.Lstat(root)
		if err != nil || !info.IsDir() {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			link := filepath.Join(root, entry.Name())
			lst, err := os.Lstat(link)
			if err != nil || lst.Mode()&os.ModeSymlink == 0 {
				continue
			}
			target, err := filepath.EvalSymlinks(link)
			if err != nil {
				continue
			}
			target = filepath.Clean(target)
			if target == root || pathWithin(root, target) {
				continue
			}
			add(target, true)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) < len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}
