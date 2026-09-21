package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSetBuildMetadataExposesTheCompiledGoToolchain(t *testing.T) {
	t.Setenv("TASKBOARD_VERSION", "")
	t.Setenv("TASKBOARD_COMMIT_SHA", "")
	t.Setenv("TASKBOARD_BUILD_TIME", "")
	t.Setenv("TASKBOARD_GO_VERSION", "")
	previous := goversion
	goversion = "go1.26.8"
	t.Cleanup(func() { goversion = previous })
	setBuildMetadata()
	if got := os.Getenv("TASKBOARD_GO_VERSION"); got != "go1.26.8" {
		t.Fatalf("TASKBOARD_GO_VERSION = %q, want the compiled toolchain", got)
	}
}

func TestSetBuildMetadataReleaseOverridesStaleEnvironment(t *testing.T) {
	t.Setenv("TASKBOARD_VERSION", "v0.1.46")
	t.Setenv("TASKBOARD_COMMIT_SHA", "old-commit")
	t.Setenv("TASKBOARD_BUILD_TIME", "old-time")
	previousVersion, previousCommit, previousBuiltAt := version, commit, builtAt
	version, commit, builtAt = "v0.1.48", "new-commit", "new-time"
	t.Cleanup(func() { version, commit, builtAt = previousVersion, previousCommit, previousBuiltAt })

	setBuildMetadata()
	if got := os.Getenv("TASKBOARD_VERSION"); got != "v0.1.48" {
		t.Fatalf("TASKBOARD_VERSION = %q, want embedded release", got)
	}
	if got := os.Getenv("TASKBOARD_COMMIT_SHA"); got != "new-commit" {
		t.Fatalf("TASKBOARD_COMMIT_SHA = %q, want embedded commit", got)
	}
	if got := os.Getenv("TASKBOARD_BUILD_TIME"); got != "new-time" {
		t.Fatalf("TASKBOARD_BUILD_TIME = %q, want embedded build time", got)
	}
}

func TestStreamingPathsBypassTheOrdinaryRequestTimeout(t *testing.T) {
	for _, path := range []string{"/events", "/mcp"} {
		if !isStreamingPath(path) {
			t.Fatalf("%s must be treated as streaming", path)
		}
	}
	for _, path := range []string{"/", "/runs", "/mcp/other", "/events/other", updateInstallPath} {
		if isStreamingPath(path) {
			t.Fatalf("%s must retain a request timeout", path)
		}
	}
}

func TestUpdateInstallPathUsesADedicatedTimeout(t *testing.T) {
	if !isUpdateInstallPath(updateInstallPath) {
		t.Fatal("update install must be classified as a long-running route")
	}
	for _, path := range []string{"/", "/runs", "/api/v1/settings/updates", "/api/v1/settings/updates/install/other", "/events", "/mcp"} {
		if isUpdateInstallPath(path) {
			t.Fatalf("%s must not inherit the update-install timeout", path)
		}
	}
}

func TestRequestTimeoutDoesNotKillUpdateInstallAtTheOrdinaryDeadline(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(80 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	srv := httptest.NewServer(requestTimeoutWith(inner, 20*time.Millisecond, time.Second))
	t.Cleanup(srv.Close)
	client := &http.Client{Timeout: time.Second}

	ordinary, err := client.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("ordinary request: %v", err)
	}
	ordinaryBody, _ := io.ReadAll(ordinary.Body)
	ordinary.Body.Close()
	if ordinary.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(ordinaryBody), "request timed out") {
		t.Fatalf("ordinary status = %d body = %q, want 503 timeout", ordinary.StatusCode, ordinaryBody)
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+updateInstallPath, strings.NewReader(`{"confirm":true}`))
	if err != nil {
		t.Fatal(err)
	}
	install, err := client.Do(req)
	if err != nil {
		t.Fatalf("update install request: %v", err)
	}
	installBody, _ := io.ReadAll(install.Body)
	install.Body.Close()
	if install.StatusCode != http.StatusOK || string(installBody) != "ok" {
		t.Fatalf("update install status = %d body = %q, want 200 after exceeding the ordinary 30s budget", install.StatusCode, installBody)
	}
}

func TestUpdateInstallStillHasADedicatedTimeout(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(80 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	srv := httptest.NewServer(requestTimeoutWith(inner, 20*time.Millisecond, 20*time.Millisecond))
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodPost, srv.URL+updateInstallPath, strings.NewReader(`{"confirm":true}`))
	if err != nil {
		t.Fatal(err)
	}
	res, err := (&http.Client{Timeout: time.Second}).Do(req)
	if err != nil {
		t.Fatalf("update install request: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "request timed out") {
		t.Fatalf("status = %d body = %q, want the dedicated install timeout to still fire", res.StatusCode, body)
	}
}
