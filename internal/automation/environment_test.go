package automation

import (
	"strings"
	"taskboard/internal/domain"
	"testing"
)

func TestAgentEnvironmentDoesNotExposeServiceSecrets(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://secret")
	t.Setenv("TASKBOARD_INTERNAL_KEY", "secret")
	t.Setenv("TEST_PROVIDER_TOKEN", "provider-secret")
	env := strings.Join(agentEnvironment(""), "\n")
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
	if strings.Contains(env, "TEST_PROVIDER_TOKEN=provider-secret") {
		t.Fatal("service provider secret was passed through")
	}
}

func TestSecretValueForEnvRequiresExplicitAssignment(t *testing.T) {
	values := []domain.SecretValue{{ID: "secret-1", EnvName: "ASSIGNED", Value: "value"}}
	if got, ok := secretValueForEnv(values, "UNASSIGNED"); ok || got.Value != "" {
		t.Fatal("unassigned environment was selected")
	}
	if got, ok := secretValueForEnv(values, "ASSIGNED"); !ok || got.ID != "secret-1" {
		t.Fatal("assigned environment was not selected")
	}
}
