package automation

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"taskboard/internal/domain"
	"testing"
)

func TestDiscoverCodexCacheReadsModelsAndEfforts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	cache := `{"models":[{"slug":"gpt-test","supported_reasoning_levels":[{"effort":"low"},{"effort":"high"}]},{"slug":"gpt-second","supported_reasoning_levels":[{"effort":"high"}]}]}`
	if err := os.WriteFile(filepath.Join(home, "models_cache.json"), []byte(cache), 0o600); err != nil {
		t.Fatal(err)
	}

	got, ok := discoverCodexCache()
	if !ok {
		t.Fatal("expected a usable Codex model cache")
	}
	if !reflect.DeepEqual(got.Models, []string{"gpt-test", "gpt-second"}) {
		t.Fatalf("models = %#v", got.Models)
	}
	if !reflect.DeepEqual(got.Efforts, []string{"low", "high"}) {
		t.Fatalf("efforts = %#v", got.Efforts)
	}
	if !reflect.DeepEqual(got.ModelEfforts["gpt-test"], []string{"low", "high"}) {
		t.Fatalf("model efforts = %#v", got.ModelEfforts)
	}
	if !got.Supports("gpt-test", "high") || got.Supports("gpt-test", "xhigh") || got.Supports("missing", "high") {
		t.Fatalf("cache capability confirmation = %#v", got)
	}
}

func TestDiscoverProviderCapabilitiesPausesWithoutConfirmedModels(t *testing.T) {
	got := DiscoverProviderCapabilities(context.Background(), domain.ProviderSetting{Provider: "openai"})
	if got.Confirmed() || !strings.Contains(got.Error, "keine bestätigten Modelle") {
		t.Fatalf("unconfirmed provider discovery = %#v", got)
	}
}

func TestDiscoverProviderCapabilitiesUsesGrokbotOptions(t *testing.T) {
	got := DiscoverProviderCapabilities(context.Background(), domain.ProviderSetting{Provider: "grokbot", Options: `{"models":["grok-4"],"efforts":["high"]}`})
	if !got.Confirmed() || !got.Supports("grok-4", "high") || got.Supports("grok-4", "xhigh") {
		t.Fatalf("grokbot discovery = %#v", got)
	}
}
