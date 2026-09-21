package container

import "testing"

func TestAgentBaseImagePinsReleaseVersion(t *testing.T) {
	t.Setenv("TASKBOARD_VERSION", "v0.1.45")
	if got, want := AgentBaseImage(), "ghcr.io/ralphschuler/shipyard-agent-base:v0.1.45"; got != want {
		t.Fatalf("image = %s, want %s", got, want)
	}
	t.Setenv("TASKBOARD_VERSION", "  v0.1.45-rc.1 ")
	if got, want := AgentBaseImage(), "ghcr.io/ralphschuler/shipyard-agent-base:v0.1.45-rc.1"; got != want {
		t.Fatalf("prerelease image = %s, want %s", got, want)
	}
}

func TestAgentBaseImageUsesLatestAsLastResort(t *testing.T) {
	for _, version := range []string{"", "development", "1.2.0", "v0.1", "latest"} {
		t.Run(version, func(t *testing.T) {
			t.Setenv("TASKBOARD_VERSION", version)
			if got, want := AgentBaseImage(), AgentBaseLatestImage(); got != want {
				t.Fatalf("TASKBOARD_VERSION=%q image = %s, want %s", version, got, want)
			}
		})
	}
}
