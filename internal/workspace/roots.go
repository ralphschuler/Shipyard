// Package workspace centralizes the on-disk roots used for managed Git
// checkouts and isolated agent worktrees. The legacy locations remain the
// default so existing deployments upgrade without moving data.
package workspace

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	legacyProjectsRoot = "/home/agent/.taskboard-projects"
	legacyRunsRoot     = "/home/agent/.taskboard-runs"
)

// Root returns an explicitly configured persistent workspace root. It is
// intentionally server-side only: callers never accept a browser supplied
// path. An empty value keeps the legacy locations for backwards compatibility.
func Root() string {
	return strings.TrimSpace(os.Getenv("TASKBOARD_WORKSPACE_ROOT"))
}

func ProjectsRoot() string {
	if root := Root(); root != "" {
		return filepath.Join(filepath.Clean(root), "projects")
	}
	return legacyProjectsRoot
}

func RunsRoot() string {
	if root := Root(); root != "" {
		return filepath.Join(filepath.Clean(root), "runs")
	}
	return legacyRunsRoot
}

func IntegrationsRoot() string {
	if root := Root(); root != "" {
		return filepath.Join(filepath.Clean(root), "integrations")
	}
	return ""
}
