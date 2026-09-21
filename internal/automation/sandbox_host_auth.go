package automation

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	sandboxCLIHome = "/tmp/shipyard-home"

	hostCLIAuthMountedLog     = "Host-CLI-Login eingebunden (nur lesen): "
	hostCLIAuthMissingWarning = "Kein Host-CLI-Login und kein zugeordnetes Secret; Provider-Authentifizierung fehlt"
)

// hostCLIAuthPlan is the minimal, read-only set of host CLI login files to
// expose inside the CLI sandbox HOME. Codex, Claude, and Grok write sessions,
// logs, and caches under their home directories, so the host tree is never
// bind-mounted as a whole: that would both block those writes on a read-only
// mount and pull large cache/log directories into the sandbox. A missing
// directory, or a directory with none of the allowlisted files, adds no bind
// and does not fail container start.
type hostCLIAuthPlan struct {
	Names    []string
	DestDirs []string
	Binds    []sandboxMappedBind
	Env      []sandboxEnvVar
}

type sandboxMappedBind struct {
	Source string
	Dest   string
}

type sandboxEnvVar struct {
	Key   string
	Value string
}

func (p hostCLIAuthPlan) empty() bool {
	return len(p.Binds) == 0
}

func (p hostCLIAuthPlan) infoLog() string {
	if len(p.Names) == 0 {
		return ""
	}
	return hostCLIAuthMountedLog + strings.Join(p.Names, ", ")
}

func (p hostCLIAuthPlan) appendDirArgs(args []string) []string {
	seen := map[string]struct{}{}
	for _, dir := range p.DestDirs {
		if dir == "" {
			continue
		}
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		args = append(args, "--dir", dir)
	}
	return args
}

func (p hostCLIAuthPlan) appendBindArgs(args []string) []string {
	for _, bind := range p.Binds {
		args = append(args, "--ro-bind", bind.Source, bind.Dest)
	}
	return args
}

func (p hostCLIAuthPlan) appendEnvArgs(args []string) []string {
	for _, env := range p.Env {
		args = append(args, "--setenv", env.Key, env.Value)
	}
	return args
}

func resolveHostCLIAuth(provider, worktree string) hostCLIAuthPlan {
	var plan hostCLIAuthPlan
	includeCodex := provider == "" || provider == "codex"
	includeGrok := provider == "" || provider == "grokbot"
	includeClaude := provider == "" || provider == "claude"
	if includeCodex {
		plan.merge(collectHostCLIAuth("Codex", "CODEX_HOME", ".codex", worktree))
	}
	if includeGrok {
		plan.merge(collectHostCLIAuth("Grok", "GROK_HOME", ".grok", worktree))
	}
	if includeClaude {
		plan.merge(collectHostCLIAuth("Claude", "CLAUDE_CONFIG_DIR", ".claude", worktree, "settings.json"))
		plan.merge(collectClaudeUserFile(worktree))
	}
	return plan
}

func (p *hostCLIAuthPlan) merge(other hostCLIAuthPlan) {
	if other.empty() {
		return
	}
	seen := map[string]struct{}{}
	for _, name := range p.Names {
		seen[name] = struct{}{}
	}
	for _, name := range other.Names {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		p.Names = append(p.Names, name)
	}
	p.DestDirs = append(p.DestDirs, other.DestDirs...)
	p.Binds = append(p.Binds, other.Binds...)
	p.Env = append(p.Env, other.Env...)
}

func collectHostCLIAuth(name, envKey, dirName, worktree string, extraFiles ...string) hostCLIAuthPlan {
	hostDir := hostCLIAuthDir(envKey, dirName)
	if hostDir == "" || !safeHostAuthSource(hostDir, worktree) {
		return hostCLIAuthPlan{}
	}
	info, err := os.Stat(hostDir)
	if err != nil || !info.IsDir() {
		return hostCLIAuthPlan{}
	}
	destDir := filepath.Join(sandboxCLIHome, dirName)
	plan := hostCLIAuthPlan{
		Names:    []string{name},
		DestDirs: []string{destDir},
		Env:      []sandboxEnvVar{{Key: envKey, Value: destDir}},
	}
	for _, filename := range hostAuthFilenames(extraFiles) {
		path := absPath(filepath.Join(hostDir, filename))
		if regularFileExists(path) && safeHostAuthSource(path, worktree) {
			plan.Binds = append(plan.Binds, sandboxMappedBind{Source: path, Dest: filepath.Join(destDir, filename)})
		}
	}
	tokens := absPath(filepath.Join(hostDir, "tokens"))
	tokenInfo, tokenErr := os.Stat(tokens)
	if tokenErr == nil && safeHostAuthSource(tokens, worktree) && (tokenInfo.IsDir() || tokenInfo.Mode().IsRegular()) {
		plan.Binds = append(plan.Binds, sandboxMappedBind{Source: tokens, Dest: filepath.Join(destDir, "tokens")})
	}
	if len(plan.Binds) == 0 {
		return hostCLIAuthPlan{}
	}
	return plan
}

func hostAuthFilenames(extra []string) []string {
	seen := map[string]struct{}{}
	var names []string
	for _, name := range append([]string{"auth.json", "config.toml", "mcp_credentials.json", "credentials.json", ".credentials.json"}, extra...) {
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

// collectClaudeUserFile bind-mounts ~/.claude.json when that file exists.
// Claude Code keeps the sign-in session there, outside CLAUDE_CONFIG_DIR.
// A missing home or a missing file is skipped.
func collectClaudeUserFile(worktree string) hostCLIAuthPlan {
	userHome, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(userHome) == "" {
		return hostCLIAuthPlan{}
	}
	path := absPath(filepath.Join(userHome, ".claude.json"))
	if !regularFileExists(path) || !safeHostAuthSource(path, worktree) {
		return hostCLIAuthPlan{}
	}
	return hostCLIAuthPlan{
		Names: []string{"Claude"},
		Binds: []sandboxMappedBind{{Source: path, Dest: filepath.Join(sandboxCLIHome, ".claude.json")}},
	}
}

func absPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return abs
}

func hostCLIAuthDir(envKey, dirName string) string {
	if home := strings.TrimSpace(os.Getenv(envKey)); home != "" {
		return absPath(home)
	}
	userHome, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(userHome) == "" {
		return ""
	}
	return filepath.Join(userHome, dirName)
}

func regularFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func safeHostAuthSource(path, worktree string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	abs = filepath.Clean(abs)
	if abs == "/" {
		return false
	}
	if worktree == "" {
		return true
	}
	workAbs, err := filepath.Abs(worktree)
	if err != nil {
		return true
	}
	workAbs = filepath.Clean(workAbs)
	if abs == workAbs {
		return false
	}
	sep := string(filepath.Separator)
	return !strings.HasPrefix(abs+sep, workAbs+sep)
}
