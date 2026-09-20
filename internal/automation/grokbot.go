package automation

import (
	"encoding/json"
	"errors"
	"strings"
	"taskboard/internal/domain"
)

func validateGrokbotConfiguration(provider domain.ProviderSetting) error {
	if strings.TrimSpace(provider.Model) == "" {
		return errors.New("Grokbot-Modell fehlt in den Provider-Einstellungen")
	}
	if strings.TrimSpace(provider.SecretEnv) == "" {
		return errors.New("Grokbot Secret-Umgebungsvariable fehlt")
	}
	return nil
}

func validateGrokbotOptions(raw string) error {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(raw) == "{}" {
		return nil
	}
	var options struct {
		Models  []string `json:"models"`
		Efforts []string `json:"efforts"`
	}
	if err := json.Unmarshal([]byte(raw), &options); err != nil {
		return errors.New("Grokbot-Optionen sind ungültiges JSON")
	}
	return nil
}

// GrokbotCapabilities derives model and effort choices from provider options.
// This keeps availability installation-specific and avoids hard-coded model
// names in the agent UI.
func GrokbotCapabilities(raw string) CapabilityDiscovery {
	result := CapabilityDiscovery{Efforts: []string{"low", "medium", "high"}, Source: "Grokbot-Provideroptionen"}
	var options struct {
		Models  []string `json:"models"`
		Efforts []string `json:"efforts"`
	}
	if strings.TrimSpace(raw) == "" || json.Unmarshal([]byte(raw), &options) != nil {
		return result
	}
	result.Models = append([]string(nil), options.Models...)
	if len(options.Efforts) > 0 {
		result.Efforts = append([]string(nil), options.Efforts...)
	}
	return result
}
