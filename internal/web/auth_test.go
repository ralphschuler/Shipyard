package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"taskboard/internal/domain"
	"testing"
	"time"
)

func TestPasswordHashRoundTrip(t *testing.T) {
	hash := passwordHash("correct-horse-battery-staple")
	if !passwordMatches(hash, "correct-horse-battery-staple") {
		t.Fatal("valid password was rejected")
	}
	if passwordMatches(hash, "not-the-password") {
		t.Fatal("invalid password was accepted")
	}
}

func TestSameOrigin(t *testing.T) {
	request := httptest.NewRequest("POST", "https://codex.local/boards", nil)
	request.Host = "codex.local"
	request.Header.Set("Origin", "https://codex.local")
	if !sameOrigin(request) {
		t.Fatal("matching Origin was rejected")
	}
	request.Header.Set("Origin", "https://evil.example")
	if sameOrigin(request) {
		t.Fatal("cross-origin request was accepted")
	}
	request.Header.Del("Origin")
	request.Header.Set("Referer", "https://codex.local/boards")
	if !sameOrigin(request) {
		t.Fatal("matching Referer was rejected")
	}
}

func TestAdminArea(t *testing.T) {
	if !isAdminArea("/settings/providers") || !isAdminArea("/agents") || !isAdminArea("/audit") {
		t.Fatal("admin areas must be protected")
	}
	if isAdminArea("/boards") || isAdminArea("/account") {
		t.Fatal("regular areas must not require admin")
	}
}

func TestTrustedProxyRequest(t *testing.T) {
	t.Setenv("TASKBOARD_PROXY_SSO_SECRET", "proxy-secret")
	t.Setenv("TASKBOARD_PROXY_SSO_EMAIL", "owner@example.test")
	request := httptest.NewRequest("GET", "https://codex.local/", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	request.Header.Set("X-Taskboard-Proxy-Email", "owner@example.test")
	request.Header.Set("X-Taskboard-Proxy-Secret", "proxy-secret")
	if !trustedProxyRequest(request) {
		t.Fatal("valid trusted proxy request was rejected")
	}
	request.Header.Set("X-Taskboard-Proxy-Secret", "wrong")
	if trustedProxyRequest(request) {
		t.Fatal("invalid proxy secret was accepted")
	}
}

func TestValidCSRF(t *testing.T) {
	session := domain.Session{CSRFToken: "expected-token"}
	request := httptest.NewRequest(http.MethodPost, "https://codex.local/boards", strings.NewReader("csrf_token=expected-token"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if !validCSRF(request, session) {
		t.Fatal("matching form CSRF token was rejected")
	}
	request = httptest.NewRequest(http.MethodPost, "https://codex.local/boards", nil)
	request.Header.Set("X-CSRF-Token", "wrong-token")
	if validCSRF(request, session) {
		t.Fatal("incorrect CSRF token was accepted")
	}
}

func TestParseRemoteDefaultBranch(t *testing.T) {
	branch, err := parseRemoteDefaultBranch("ref: refs/heads/master\tHEAD\n2194fd\tHEAD\n")
	if err != nil || branch != "master" {
		t.Fatalf("branch=%q err=%v, want master", branch, err)
	}
	if _, err := parseRemoteDefaultBranch("2194fd\tHEAD\n"); err == nil {
		t.Fatal("missing symbolic HEAD was accepted")
	}
}

func TestLoginThrottle(t *testing.T) {
	throttle := newLoginThrottle()
	at := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	for range 6 {
		throttle.failed("192.0.2.10", at)
	}
	if !throttle.blocked("192.0.2.10", at.Add(time.Minute)) {
		t.Fatal("six failed attempts must trigger a temporary block")
	}
	throttle.succeeded("192.0.2.10")
	if throttle.blocked("192.0.2.10", at.Add(time.Minute)) {
		t.Fatal("successful login must clear the temporary block")
	}
}
