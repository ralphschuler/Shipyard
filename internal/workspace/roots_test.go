package workspace

import (
	"os"
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
}

func TestValidateConfiguredWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TASKBOARD_WORKSPACE_ROOT", root)
	if err := os.WriteFile(MarkerPath(), []byte("shipyard workspace\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	status, err := Validate()
	if err != nil || !status.Ready() {
		t.Fatalf("Validate() = %#v, %v; want ready workspace", status, err)
	}
	for _, path := range []string{status.Projects, status.Runs, status.Integrations} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("managed directory %q missing: %v", path, err)
		}
	}
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
