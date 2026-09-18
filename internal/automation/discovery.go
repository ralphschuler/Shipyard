package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// CapabilityDiscovery is deliberately ephemeral: it is a read-only probe of
// the configured Codex executable and contains no credentials.
type CapabilityDiscovery struct {
	Models  []string  `json:"models"`
	Efforts []string  `json:"efforts"`
	Source  string    `json:"source"`
	At      time.Time `json:"at"`
	Error   string    `json:"error,omitempty"`
}

// DiscoverCodex runs the provider command's machine-readable discovery
// endpoint when available. A failed probe is explicit and safe: the caller
// must keep the agent paused instead of silently selecting a provider default.
func DiscoverCodex(ctx context.Context, command string) CapabilityDiscovery {
	result := CapabilityDiscovery{Efforts: []string{"low", "medium", "high", "xhigh"}, Source: "codex discovery", At: time.Now()}
	parts := strings.Fields(command)
	if len(parts) == 0 {
		parts = []string{"codex"}
	}
	out, err := exec.CommandContext(ctx, parts[0], append(parts[1:], "models", "--json")...).Output()
	if err != nil {
		result.Error = fmt.Sprintf("Discovery nicht erreichbar: %v", err)
		return result
	}
	var payload struct {
		Models []string `json:"models"`
	}
	if err := json.Unmarshal(out, &payload); err != nil || len(payload.Models) == 0 {
		if err == nil {
			err = fmt.Errorf("keine Modelle gemeldet")
		}
		result.Error = "Discovery ungültig: " + err.Error()
		return result
	}
	result.Models = payload.Models
	return result
}
