package automation

import (
	"strings"
	"testing"
)

func TestAgentEnvironmentDoesNotExposeServiceSecrets(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://secret")
	t.Setenv("TASKBOARD_INTERNAL_KEY", "secret")
	t.Setenv("TEST_PROVIDER_TOKEN", "provider-secret")
	env := strings.Join(agentEnvironment("TEST_PROVIDER_TOKEN"), "\n")
	lines := strings.Split(env, "\n")
	containsKey := func(key string) bool {
		for _, line := range lines {
			if strings.HasPrefix(line, key+"=") {
				return true
			}
		}
		return false
	}
	if containsKey("DATABASE_URL") || containsKey("TASKBOARD_INTERNAL_KEY") {
		t.Fatal("agent environment inherited a service secret")
	}
	if !strings.Contains(env, "TEST_PROVIDER_TOKEN=provider-secret") {
		t.Fatal("configured provider secret was not passed through")
	}
}
