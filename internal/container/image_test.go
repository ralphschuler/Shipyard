package container

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestAgentBaseDockerfileInstallsProviderCLIs(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "agent-base", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, required := range []string{
		"ARG CODEX_CLI_VERSION=0.155.1",
		"ARG CLAUDE_CODE_VERSION=2.1.278",
		"ARG GROK_CLI_VERSION=1.0.40",
		`"@openai/codex@${CODEX_CLI_VERSION}"`,
		`"@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}"`,
		`"@xai-official/grok@${GROK_CLI_VERSION}"`,
		"test -x /usr/local/bin/codex",
		"test -x /usr/local/bin/claude",
		"test -x /usr/local/bin/grok",
		"PATH=/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("agent base Dockerfile missing %q", required)
		}
	}
	dev, err := os.ReadFile(filepath.Join("..", "..", ".devcontainer", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	devText := string(dev)
	if strings.Contains(devText, "@openai/codex") || strings.Contains(devText, "@anthropic-ai/claude-code") || strings.Contains(devText, "@xai-official/grok") {
		t.Fatal("dev container Dockerfile installs a second copy of the provider CLIs")
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
