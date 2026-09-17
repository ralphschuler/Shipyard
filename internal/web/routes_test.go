package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"taskboard/internal/updates"
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
		"/settings/updates",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		_, pattern := mux.Handler(req)
		if pattern != "GET "+path {
			t.Fatalf("%s: registered pattern = %q, want %q", path, pattern, "GET "+path)
		}
	}
}

func TestUpdatesAPIFailsClosedForUnverifiedRelease(t *testing.T) {
	t.Setenv("TASKBOARD_VERSION", "1.2.0")
	t.Setenv("TASKBOARD_COMMIT_SHA", "abc123")
	t.Setenv("TASKBOARD_GITHUB_API_URL", "http://updates.invalid")
	res := httptest.NewRecorder()
	(&App{}).updatesAPI(res, httptest.NewRequest(http.MethodGet, "/api/v1/settings/updates", nil))
	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "unavailable" || payload["installable"] != false {
		t.Fatalf("payload = %#v", payload)
	}
}

type updateRoundTripper func(*http.Request) (*http.Response, error)

func (f updateRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUpdatesAPIUsesGitHubMetadataOnly(t *testing.T) {
	t.Setenv("TASKBOARD_VERSION", "1.2.0")
	t.Setenv("TASKBOARD_UPDATE_VERSION", "9.9.9")
	t.Setenv("TASKBOARD_UPDATE_VERIFIED", "true")
	commit := "0123456789012345678901234567890123456789"
	transport := updateRoundTripper(func(r *http.Request) (*http.Response, error) {
		var body string
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			body = fmt.Sprintf(`{"tag_name":"v1.3.0","html_url":"https://github.com/ralphschuler/Shipyard/releases/tag/v1.3.0","published_at":"2026-09-17T10:00:00Z","target_commitish":"master","assets":[{"name":"shipyard-linux-%s","browser_download_url":"https://github.com/ralphschuler/Shipyard/releases/download/v1.3.0/shipyard-linux-%s","digest":"sha256:0123456789012345678901234567890123456789012345678901234567890123"}]}`, runtime.GOARCH, runtime.GOARCH)
		case strings.HasSuffix(r.URL.Path, "/commits/v1.3.0"):
			body = fmt.Sprintf(`{"sha":"%s","commit":{"verification":{"verified":true}}}`, commit)
		default:
			return nil, fmt.Errorf("unexpected GitHub path %s", r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	old := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: transport}
	t.Cleanup(func() { http.DefaultClient = old })
	res := httptest.NewRecorder()
	(&App{}).updatesAPI(res, httptest.NewRequest(http.MethodGet, "/api/v1/settings/updates", nil))
	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "update_available" || payload["installable"] != true {
		t.Fatalf("payload = %#v", payload)
	}
	release := payload["release"].(map[string]any)
	if release["version"] != "v1.3.0" {
		t.Fatalf("release was not sourced from GitHub: %#v", release)
	}
}

func TestInstallUpdateRequiresExplicitConfirmation(t *testing.T) {
	for name, body := range map[string]string{"missing": "{}", "false": `{"confirm":false}`} {
		t.Run(name, func(t *testing.T) {
			res := httptest.NewRecorder()
			(&App{}).installUpdateAPI(res, httptest.NewRequest(http.MethodPost, "/api/v1/settings/updates/install", bytes.NewBufferString(body)))
			if res.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", res.Code, http.StatusBadRequest)
			}
		})
	}
	res := httptest.NewRecorder()
	(&App{}).installUpdateAPI(res, httptest.NewRequest(http.MethodPost, "/api/v1/settings/updates/install", bytes.NewBufferString(`{"confirm":true}`)))
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("confirmed status = %d, want %d", res.Code, http.StatusServiceUnavailable)
	}
}

func TestInstallUpdateRunsOnlyAfterVerifiedSnapshot(t *testing.T) {
	t.Setenv("TASKBOARD_VERSION", "1.2.0")
	commit := "0123456789012345678901234567890123456789"
	transport := updateRoundTripper(func(r *http.Request) (*http.Response, error) {
		var body string
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			body = fmt.Sprintf(`{"tag_name":"v1.3.0","html_url":"https://github.com/ralphschuler/Shipyard/releases/tag/v1.3.0","published_at":"2026-09-17T10:00:00Z","target_commitish":"master","assets":[{"name":"shipyard-linux-%s","browser_download_url":"https://github.com/ralphschuler/Shipyard/releases/download/v1.3.0/shipyard-linux-%s","digest":"sha256:0123456789012345678901234567890123456789012345678901234567890123"}]}`, runtime.GOARCH, runtime.GOARCH)
		case strings.HasSuffix(r.URL.Path, "/commits/v1.3.0"):
			body = fmt.Sprintf(`{"sha":"%s","commit":{"verification":{"verified":true}}}`, commit)
		default:
			return nil, fmt.Errorf("unexpected GitHub path %s", r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	old := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: transport}
	t.Cleanup(func() { http.DefaultClient = old })
	called := false
	noop := func(context.Context, updates.Snapshot) error { called = true; return nil }
	app := &App{update: &updates.Orchestrator{Backup: noop, Verify: noop, Migrate: noop, Switch: noop, Restart: noop, Health: noop, Rollback: noop}}
	res := httptest.NewRecorder()
	app.installUpdateAPI(res, httptest.NewRequest(http.MethodPost, "/api/v1/settings/updates/install", bytes.NewBufferString(`{"confirm":true}`)))
	if res.Code != http.StatusOK || !called {
		t.Fatalf("status = %d, called = %v, body = %s", res.Code, called, res.Body.String())
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
