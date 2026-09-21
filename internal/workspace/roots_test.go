package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootsUseLegacyLocationsWithoutConfiguration(t *testing.T) {
	t.Setenv("TASKBOARD_WORKSPACE_ROOT", "")
	if got, want := ProjectsRoot(), legacyProjectsRoot; got != want {
		t.Fatalf("ProjectsRoot() = %q, want %q", got, want)
	}
	if got, want := RunsRoot(), legacyRunsRoot; got != want {
		t.Fatalf("RunsRoot() = %q, want %q", got, want)
	}
	if got := IntegrationsRoot(); got != "" {
		t.Fatalf("IntegrationsRoot() = %q, want empty legacy fallback", got)
	}
	if got, want := RuntimeRoot(), legacyRuntimeRoot; got != want {
		t.Fatalf("RuntimeRoot() = %q, want %q", got, want)
	}
}

func TestRootsUseConfiguredWorkspaceRoot(t *testing.T) {
	t.Setenv("TASKBOARD_WORKSPACE_ROOT", "/srv/codex/workspaces/shipyard/../shipyard")
	if got, want := ProjectsRoot(), "/srv/codex/workspaces/shipyard/projects"; got != want {
		t.Fatalf("ProjectsRoot() = %q, want %q", got, want)
	}
	if got, want := RunsRoot(), "/srv/codex/workspaces/shipyard/runs"; got != want {
		t.Fatalf("RunsRoot() = %q, want %q", got, want)
	}
	if got, want := IntegrationsRoot(), "/srv/codex/workspaces/shipyard/integrations"; got != want {
		t.Fatalf("IntegrationsRoot() = %q, want %q", got, want)
	}
	if got, want := RuntimeRoot(), "/srv/codex/workspaces/shipyard/runtime"; got != want {
		t.Fatalf("RuntimeRoot() = %q, want %q", got, want)
	}
}

func TestValidateConfiguredWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TASKBOARD_WORKSPACE_ROOT", root)
	if err := os.WriteFile(MarkerPath(), []byte("shipyard workspace\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	status, err := Validate()
	if err != nil || !status.Ready() || status.Migration {
		t.Fatalf("Validate() = %#v, %v; want ready workspace", status, err)
	}
	if _, err := os.Stat(filepath.Join(root, ".shipyard-migration.lock")); !os.IsNotExist(err) {
		t.Fatalf("preflight created a migration lock file: %v", err)
	}
	for _, path := range []string{status.Projects, status.Runs, status.Integrations, status.Runtime} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("managed directory %q missing: %v", path, err)
		}
	}
	info, err := os.Stat(status.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("runtime mode = %o, want 0700", info.Mode().Perm())
	}
}

func TestRuntimeRootIsOutsidePrivateTmp(t *testing.T) {
	t.Setenv("TASKBOARD_WORKSPACE_ROOT", "/srv/codex/workspaces/shipyard")
	if got, want := RuntimeRoot(), "/srv/codex/workspaces/shipyard/runtime"; got != want {
		t.Fatalf("RuntimeRoot() = %q, want %q", got, want)
	}
	if privateTmpPath(RuntimeRoot()) {
		t.Fatalf("configured runtime root %s is inside PrivateTmp", RuntimeRoot())
	}
	t.Setenv("TASKBOARD_WORKSPACE_ROOT", "")
	if got, want := RuntimeRoot(), legacyRuntimeRoot; got != want {
		t.Fatalf("RuntimeRoot() = %q, want %q", got, want)
	}
	if privateTmpPath(RuntimeRoot()) {
		t.Fatalf("legacy runtime root %s is inside PrivateTmp", RuntimeRoot())
	}
	for _, private := range []string{"/tmp", "/var/tmp"} {
		if privateTmpPath(filepath.Join(private, "shipyard-agent-home-1")) != true {
			t.Fatalf("expected %s to be treated as PrivateTmp", private)
		}
	}
}

func privateTmpPath(path string) bool {
	clean := filepath.Clean(path)
	for _, root := range []string{"/tmp", "/var/tmp"} {
		if clean == root || strings.HasPrefix(clean, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func TestValidateRejectsRootPath(t *testing.T) {
	t.Setenv("TASKBOARD_WORKSPACE_ROOT", "/")
	if _, err := Validate(); err == nil {
		t.Fatal("Validate() accepted filesystem root")
	}
}

func TestValidateRejectsUnmarkedConfiguredRoot(t *testing.T) {
	t.Setenv("TASKBOARD_WORKSPACE_ROOT", t.TempDir())
	status, err := Validate()
	if err == nil || status.Error == "" || status.Ready() {
		t.Fatalf("Validate() = %#v, %v; want actionable marker error", status, err)
	}
}

func TestValidateRejectsNFSMarkerOnNonNFSStorage(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TASKBOARD_WORKSPACE_ROOT", root)
	if err := os.WriteFile(MarkerPath(), []byte("shipyard workspace\nstorage=nfs\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	status, err := Validate()
	if err == nil || status.Ready() || status.Error == "" {
		t.Fatalf("Validate() = %#v, %v; want storage mismatch", status, err)
	}
}

func TestValidateMissingRootDoesNotCreateLocalFallback(t *testing.T) {
	root := t.TempDir() + "/missing"
	t.Setenv("TASKBOARD_WORKSPACE_ROOT", root)
	_, err := Validate()
	if err == nil {
		t.Fatal("Validate() accepted missing root")
	}
	if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
		t.Fatalf("Validate() created missing root: %v", statErr)
	}
}
