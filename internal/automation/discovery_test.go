package automation

import (
	"os"
	"path/filepath"
	"reflect"
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
}
