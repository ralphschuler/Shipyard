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

func TestRootRedirectsToCanonicalAppAndPreservesQuery(t *testing.T) {
	mux := http.NewServeMux()
	(&App{}).Register(mux)
	request := httptest.NewRequest(http.MethodGet, "/?board=123&filter=open", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusPermanentRedirect {
		t.Fatalf("root status = %d, want %d", response.Code, http.StatusPermanentRedirect)
	}
	if got := response.Header().Get("Location"); got != "/app/?board=123&filter=open" {
		t.Fatalf("root Location = %q, want query-preserving app URL", got)
	}
	if strings.Contains(response.Body.String(), "Dashboard") {
		t.Fatal("root response rendered the legacy dashboard")
	}
}

func TestAppClientRoutesUseIndexButMissingAssetsStay404(t *testing.T) {
	mux := http.NewServeMux()
	(&App{}).Register(mux)
	for _, path := range []string{"/app/tasks/123", "/app/settings/updates"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `<div id="root">`) {
			t.Fatalf("%s: status=%d, body is not the app shell", path, response.Code)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/app/missing.js", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing asset status = %d, want %d", response.Code, http.StatusNotFound)
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
	if payload["status"] != "unverified" || payload["installable"] != false {
		t.Fatalf("payload = %#v", payload)
	}
	if !strings.Contains(payload["reason"].(string), "TASKBOARD_GITHUB_RELEASE_ALLOWLIST") {
		t.Fatalf("reason = %#v", payload["reason"])
	}
	if checkedAt, ok := payload["checked_at"].(string); !ok || strings.TrimSpace(checkedAt) == "" {
		t.Fatalf("checked_at = %#v, want a timestamp", payload["checked_at"])
	}
}

type updateRoundTripper func(*http.Request) (*http.Response, error)

func (f updateRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestUpdatesAPIUsesGitHubMetadataOnly(t *testing.T) {
	t.Setenv("TASKBOARD_VERSION", "1.2.0")
	t.Setenv("TASKBOARD_GITHUB_RELEASE_ALLOWLIST", "v1.3.0")
	t.Setenv("TASKBOARD_GITHUB_TOKEN", "read-only-token")
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
		case strings.HasSuffix(r.URL.Path, "/compare/master..."+commit):
			body = `{"status":"behind","ahead_by":0,"behind_by":1}`
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

func TestActiveUpdateRunsQueryExcludesTheConfirmingSession(t *testing.T) {
	query, args := activeUpdateRunsQuery("current-session-token-hash")
	if len(args) != 0 {
		t.Fatalf("query args = %#v, want no browser-session arguments", args)
	}
	if strings.Contains(query, "user_sessions") {
		t.Fatalf("query incorrectly treats browser sessions as active work: %s", query)
	}
}

func TestInstallUpdateRunsOnlyAfterVerifiedSnapshot(t *testing.T) {
	t.Setenv("TASKBOARD_VERSION", "1.2.0")
	t.Setenv("TASKBOARD_GITHUB_RELEASE_ALLOWLIST", "v1.3.0")
	t.Setenv("TASKBOARD_GITHUB_TOKEN", "read-only-token")
	commit := "0123456789012345678901234567890123456789"
	transport := updateRoundTripper(func(r *http.Request) (*http.Response, error) {
		var body string
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			body = fmt.Sprintf(`{"tag_name":"v1.3.0","html_url":"https://github.com/ralphschuler/Shipyard/releases/tag/v1.3.0","published_at":"2026-09-17T10:00:00Z","target_commitish":"master","assets":[{"name":"shipyard-linux-%s","browser_download_url":"https://github.com/ralphschuler/Shipyard/releases/download/v1.3.0/shipyard-linux-%s","digest":"sha256:0123456789012345678901234567890123456789012345678901234567890123"}]}`, runtime.GOARCH, runtime.GOARCH)
		case strings.HasSuffix(r.URL.Path, "/commits/v1.3.0"):
			body = fmt.Sprintf(`{"sha":"%s","commit":{"verification":{"verified":true}}}`, commit)
		case strings.HasSuffix(r.URL.Path, "/compare/master..."+commit):
			body = `{"status":"behind","ahead_by":0,"behind_by":1}`
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
	app := &App{update: &updates.Orchestrator{Backup: noop, DownloadAndVerify: func(context.Context, string, string) ([]byte, error) { return []byte("artifact"), nil }, VerifyArtifact: func(context.Context, updates.Snapshot, []byte) error { return nil }, Verify: noop, Migrate: noop, Switch: func(context.Context, updates.Snapshot, []byte) error { called = true; return nil }, Restart: noop, Health: noop, Rollback: noop}}
	res := httptest.NewRecorder()
	app.installUpdateAPI(res, httptest.NewRequest(http.MethodPost, "/api/v1/settings/updates/install", bytes.NewBufferString(`{"confirm":true}`)))
	if res.Code != http.StatusOK || !called {
		t.Fatalf("status = %d, called = %v, body = %s", res.Code, called, res.Body.String())
	}
}

func TestUpdateInstallErrorMessageDoesNotExposeAdapterDetails(t *testing.T) {
	if got := updateInstallErrorMessage(fmt.Errorf("open /srv/shipyard/releases/token: permission denied")); strings.Contains(got, "/srv/shipyard") || strings.Contains(got, "token") {
		t.Fatalf("error message exposes adapter details: %q", got)
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
