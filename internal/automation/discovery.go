package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	if cached, ok := discoverCodexCache(); ok {
		cached.At = result.At
		return cached
	}
	parts := strings.Fields(command)
	if len(parts) == 0 {
		parts = []string{"codex"}
	}
	// Provider commands describe how to *run* an agent (normally "codex exec").
	// Reusing their trailing arguments here accidentally turns "models" into an
	// agent prompt and leaves the agent editor waiting for that run. Capability
	// discovery must invoke only the executable, and it must never keep an HTTP
	// request open indefinitely when a CLI does not support this subcommand.
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, parts[0], "models", "--json").Output()
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

// discoverCodexCache reads the CLI's local, read-only model catalogue when it
// is available. Current Codex CLI releases maintain this cache but do not
// expose a `models --json` command, so using it keeps the agent form responsive
// while still reflecting the models available to this installation.
func discoverCodexCache() (CapabilityDiscovery, bool) {
	home := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return CapabilityDiscovery{}, false
		}
		home = filepath.Join(userHome, ".codex")
	}
	data, err := os.ReadFile(filepath.Join(home, "models_cache.json"))
	if err != nil {
		return CapabilityDiscovery{}, false
	}
	var cache struct {
		Models []struct {
			Slug                     string `json:"slug"`
			SupportedReasoningLevels []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &cache); err != nil {
		return CapabilityDiscovery{}, false
	}
	models, efforts := make([]string, 0, len(cache.Models)), make([]string, 0, 6)
	seenModels, seenEfforts := map[string]bool{}, map[string]bool{}
	for _, model := range cache.Models {
		if model.Slug != "" && !seenModels[model.Slug] {
			seenModels[model.Slug] = true
			models = append(models, model.Slug)
		}
		for _, level := range model.SupportedReasoningLevels {
			if level.Effort != "" && !seenEfforts[level.Effort] {
				seenEfforts[level.Effort] = true
				efforts = append(efforts, level.Effort)
			}
		}
	}
	if len(models) == 0 {
		return CapabilityDiscovery{}, false
	}
	if len(efforts) == 0 {
		efforts = []string{"low", "medium", "high", "xhigh"}
	}
	return CapabilityDiscovery{Models: models, Efforts: efforts, Source: "Codex local model cache"}, true
}
