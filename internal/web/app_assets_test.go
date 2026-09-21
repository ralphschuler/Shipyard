package web

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strings"
	"testing"
)

func TestAppAssetsAreServedFromEmbeddedFilesystem(t *testing.T) {
	index, err := fs.ReadFile(appDist, "index.html")
	if err != nil {
		t.Fatalf("embedded app index unavailable: %v; run the frontend build before Go tests", err)
	}
	if !strings.Contains(string(index), "<div id=\"root\">") {
		t.Fatal("embedded app index does not contain the application root")
	}

	var buildInfo struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
	}
	info, err := fs.ReadFile(appDist, "build-info.json")
	if err != nil || json.Unmarshal(info, &buildInfo) != nil || buildInfo.Version == "" || buildInfo.Commit == "" {
		t.Fatalf("embedded build metadata is incomplete: %s (err: %v)", info, err)
	}

	assetPattern := regexp.MustCompile(`(?:src|href)=\"([^\"]+-[a-zA-Z0-9_-]{8,}\.[a-z0-9]+)\"`)
	matches := assetPattern.FindAllStringSubmatch(string(index), -1)
	if len(matches) == 0 {
		t.Fatal("index.html has no fingerprinted asset references")
	}
	for _, match := range matches {
		asset := strings.TrimPrefix(match[1], "/app/")
		if _, err := fs.Stat(appDist, path.Clean(asset)); err != nil {
			t.Fatalf("index references missing embedded asset %q: %v", asset, err)
		}
	}

	mux := http.NewServeMux()
	(&App{}).Register(mux)
	request := httptest.NewRequest(http.MethodGet, "/app/", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("embedded app response status = %d, want %d", response.Code, http.StatusOK)
	}
	if response.Body.String() != string(index) {
		t.Fatal("/app/ did not serve the embedded index.html")
	}

	staleRequest := httptest.NewRequest(http.MethodGet, "/app/stale-checkout.js", nil)
	staleResponse := httptest.NewRecorder()
	mux.ServeHTTP(staleResponse, staleRequest)
	if staleResponse.Code != http.StatusNotFound {
		t.Fatalf("stale external asset response status = %d, want %d", staleResponse.Code, http.StatusNotFound)
	}
}

func TestEmbeddedHealthReportsTheCompiledGoToolchain(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	EmbeddedAppHandler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["status"] != "ok" || payload["database"] != "embedded-test" {
		t.Fatalf("payload = %#v", payload)
	}
	if !strings.HasPrefix(payload["goVersion"], "go1.") {
		t.Fatalf("goVersion = %q, want a compiled go1.x toolchain", payload["goVersion"])
	}
}
