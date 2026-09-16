package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNormalizeLanguageDefaultsToGerman(t *testing.T) {
	for _, value := range []string{"", "fr", "EN"} {
		if got := normalizeLanguage(value); got != "de" {
			t.Fatalf("normalizeLanguage(%q) = %q, want de", value, got)
		}
	}
	if got := normalizeLanguage("en"); got != "en" {
		t.Fatalf("normalizeLanguage(en) = %q, want en", got)
	}
}

func TestAccountLanguageOverridesBrowserAndFallbackUsesEnglish(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Language", "en-US")
	if got := resolveLanguage("de", r); got != "de" {
		t.Fatalf("account preference = %q, want de", got)
	}
	if got := resolveLanguage("en", r); got != "en" {
		t.Fatalf("account preference = %q, want en", got)
	}
	if got := translate("de", "missing.key", "English fallback"); got != "English fallback" {
		t.Fatalf("missing translation = %q, want English fallback", got)
	}
}
