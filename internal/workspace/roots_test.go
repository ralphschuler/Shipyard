package workspace

import "testing"

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
