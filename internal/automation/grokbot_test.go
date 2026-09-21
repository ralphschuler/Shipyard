package automation

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"taskboard/internal/domain"
	"testing"
)

func TestGrokbotResponsesURLUsesXAIEndpoint(t *testing.T) {
	got, err := grokbotResponsesURL("")
	if err != nil || got != "https://api.x.ai/v1/responses" {
		t.Fatalf("endpoint = %q, err = %v", got, err)
	}
	got, err = grokbotResponsesURL("https://gateway.example/xai/v1")
	if err != nil || got != "https://gateway.example/xai/v1/responses" {
		t.Fatalf("custom endpoint = %q, err = %v", got, err)
	}
	if _, err = grokbotResponsesURL("http://gateway.example"); err == nil {
		t.Fatal("insecure endpoint was accepted")
	}
}

func TestValidateGrokbotConfigurationRequiresAPISecretAndModel(t *testing.T) {
	provider := domain.ProviderSetting{Provider: "grokbot", Model: "grok-4", SecretEnv: "XAI_API_KEY"}
	if err := validateGrokbotConfiguration(provider); err != nil {
		t.Fatalf("valid configuration rejected: %v", err)
	}
	provider.SecretEnv = ""
	if err := validateGrokbotConfiguration(provider); err == nil {
		t.Fatal("missing secret environment was accepted")
	}
	provider.SecretEnv = "XAI_API_KEY"
	provider.Model = ""
	if err := validateGrokbotConfiguration(provider); err == nil {
		t.Fatal("missing model was accepted")
	}
}

func TestGrokbotResponsesUsesBearerSecretWithoutLoggingIt(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret-value" {
			t.Errorf("authorization = %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "secret-value") {
			t.Error("secret appeared in request body")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"grok-1","output_text":"done","usage":{"input_tokens":2,"output_tokens":3,"total_tokens":5}}`)
	}))
	defer server.Close()
	previousTransport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = previousTransport }()

	result, usage, err := runGrokbotResponses(context.Background(), domain.ProviderSetting{
		Provider:  "grokbot",
		Model:     "grok-4",
		SecretEnv: "XAI_API_KEY",
		BaseURL:   server.URL,
	}, "secret-value", "hello")
	if err != nil || result != "done" || usage.TotalTokens != 5 {
		t.Fatalf("result = %q, usage = %#v, err = %v", result, usage, err)
	}
}

func TestGrokbotResponsesRedactsSecretOnAPIError(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid key secret-value"}}`)
	}))
	defer server.Close()
	previousTransport := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = previousTransport }()

	_, _, err := runGrokbotResponses(context.Background(), domain.ProviderSetting{
		Provider:  "grokbot",
		Model:     "grok-4",
		SecretEnv: "XAI_API_KEY",
		BaseURL:   server.URL,
	}, "secret-value", "hello")
	if err == nil {
		t.Fatal("expected API error")
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("secret leaked in error: %v", err)
	}
	if !strings.Contains(err.Error(), "[redacted]") || !strings.Contains(err.Error(), "Grokbot-Anfrage fehlgeschlagen") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateGrokbotOptionsRejectsInvalidJSON(t *testing.T) {
	if err := validateGrokbotOptions(`{"models":["grok-4"],"efforts":["low"]}`); err != nil {
		t.Fatalf("valid options rejected: %v", err)
	}
	if err := validateGrokbotOptions(""); err != nil {
		t.Fatalf("empty options rejected: %v", err)
	}
	if err := validateGrokbotOptions("{"); err == nil {
		t.Fatal("invalid JSON was accepted")
	}
}

func TestUsesHTTPResponsesAdapterForGrokbotWithoutCommand(t *testing.T) {
	httpProvider := domain.ProviderSetting{Provider: "grokbot", Command: ""}
	if !usesHTTPResponsesAdapter(httpProvider) {
		t.Fatal("empty grokbot command must use the HTTP adapter")
	}
	if usesHTTPResponsesAdapter(domain.ProviderSetting{Provider: "grokbot", Command: "grok --always-approve"}) {
		t.Fatal("configured grokbot command must use the CLI adapter")
	}
	if !usesHTTPResponsesAdapter(domain.ProviderSetting{Provider: "openai", Command: "ignored"}) {
		t.Fatal("openai must stay on the HTTP adapter")
	}
	if usesHTTPResponsesAdapter(domain.ProviderSetting{Provider: "codex", Command: "codex exec"}) {
		t.Fatal("codex must stay on the CLI adapter")
	}
}

func TestGrokbotCapabilitiesRecordsDiscoveryTime(t *testing.T) {
	got := GrokbotCapabilities(`{"models":["grok-4"],"efforts":["medium"]}`)
	if got.At.IsZero() || !got.Confirmed() || !got.Supports("grok-4", "medium") {
		t.Fatalf("capabilities = %#v", got)
	}
}
