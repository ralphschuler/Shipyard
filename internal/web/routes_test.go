package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Parent navigation paths are part of the public UI contract. Settings uses
// tabs, so its parent path must remain a valid bookmark instead of falling
// through to the default 404 handler.
func TestSettingsParentRoutesRedirectToDefaultTab(t *testing.T) {
	mux := http.NewServeMux()
	(&App{}).Register(mux)
	for _, path := range []string{"/settings", "/settings/"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		if res.Code != http.StatusSeeOther {
			t.Fatalf("%s: status = %d, want %d", path, res.Code, http.StatusSeeOther)
		}
		if got := res.Header().Get("Location"); got != "/settings/providers" {
			t.Fatalf("%s: Location = %q, want provider settings", path, got)
		}
	}
}

// Every settings tab is a public browser URL. Verify registration itself so a
// template link cannot quietly regress to the default 404 handler.
func TestSettingsTabsAreRegistered(t *testing.T) {
	mux := http.NewServeMux()
	(&App{}).Register(mux)
	for _, path := range []string{
		"/settings/providers",
		"/settings/agent-policy",
		"/settings/appearance",
		"/settings/integrations",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		_, pattern := mux.Handler(req)
		if pattern != "GET "+path {
			t.Fatalf("%s: registered pattern = %q, want %q", path, pattern, "GET "+path)
		}
	}
}

func TestRunStatusLabelsArePlainGerman(t *testing.T) {
	for status, want := range map[string]string{
		"queued": "Wartet", "running": "Läuft", "succeeded": "Erfolgreich",
		"failed": "Fehlgeschlagen", "cancelled": "Abgebrochen", "partial": "Teilweise beendet",
	} {
		if got := runStatusLabel(status); got != want {
			t.Fatalf("%s: got %q, want %q", status, got, want)
		}
	}
}

func TestGateStatusLabelsArePlainGerman(t *testing.T) {
	for status, want := range map[string]string{
		"passed": "Bestanden", "failed": "Fehlgeschlagen", "pending": "Ausstehend", "skipped": "Übersprungen",
	} {
		if got := gateStatusLabel(status); got != want {
			t.Fatalf("%s: got %q, want %q", status, got, want)
		}
	}
}
