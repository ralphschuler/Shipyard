package web

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
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

func TestLanguageDefaultsToGermanAndHonorsPreference(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "https://codex.local/", nil)
	if got := language(request); got != "de" {
		t.Fatalf("default language = %q, want de", got)
	}
	request.Header.Set("Accept-Language", "en-US,en;q=0.9")
	if got := language(request); got != "en" {
		t.Fatalf("header language = %q, want en", got)
	}
	request.AddCookie(&http.Cookie{Name: "shipyard_language", Value: "de"})
	if got := language(request); got != "de" {
		t.Fatalf("cookie language = %q, want de", got)
	}
	request.AddCookie(&http.Cookie{Name: "shipyard_language", Value: "fr"})
	if got := language(request); got != "de" {
		t.Fatalf("invalid cookie language = %q, want de fallback", got)
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

func TestReviewAdminAPIRouteClassification(t *testing.T) {
	admin := []string{
		"/settings/providers",
		"/settings/updates",
		"/agents",
		"/agents/templates",
		"/audit",
		"/skills",
		"/skills/skills-sh/install",
		"/automations",
		"/schedules",
		"/webhooks",
		"/sandbox-profiles",
		"/sandbox-profiles/strict",
		"/api/v1/settings/updates/install",
		"/api/v1/skills/install",
		"/api/v1/audit",
		"/api/v1/settings/providers",
		"/api/v1/agents",
		"/api/v1/agents/agent-1",
		"/api/v1/settings/sandbox-profiles",
		"/api/v1/skills",
		"/api/v1/skills/search",
		"/api/v1/automations",
		"/api/v1/schedules",
		"/api/v1/webhooks",
		"/api/v1/settings/updates",
		"/api/v1/settings/capabilities",
		"/api/v1/settings/workspace",
		"/api/v1/settings/agent-policy",
		"/api/v1/settings/appearance",
		"/api/v1/settings/integrations",
	}
	for _, path := range admin {
		if !isAdminArea(path) {
			t.Errorf("%s: isAdminArea = false, want true", path)
		}
	}
	nonAdmin := []string{
		"/boards",
		"/account",
		"/projects",
		"/runs",
		"/app/settings/updates",
		"/api/v1/boards",
		"/api/v1/account",
		"/api/v1/projects",
		"/api/v1/runs",
		"/api/v1/dashboard",
		"/api/v1/memory",
		"/api/v10/settings/providers",
	}
	for _, path := range nonAdmin {
		if isAdminArea(path) {
			t.Errorf("%s: isAdminArea = true, want false", path)
		}
	}
}

func TestRoleMatrixForLegacyJSONAndSandboxRoutes(t *testing.T) {
	type endpoint struct {
		method, path string
		adminOnly    bool
	}
	endpoints := []endpoint{
		{http.MethodGet, "/settings/providers", true},
		{http.MethodPost, "/settings/providers/codex", true},
		{http.MethodGet, "/api/v1/settings/providers", true},
		{http.MethodGet, "/settings/updates", true},
		{http.MethodPost, "/api/v1/settings/updates/install", true},
		{http.MethodGet, "/skills", true},
		{http.MethodPost, "/skills/skills-sh/install", true},
		{http.MethodGet, "/api/v1/skills", true},
		{http.MethodPost, "/api/v1/skills/install", true},
		{http.MethodGet, "/audit", true},
		{http.MethodGet, "/api/v1/audit", true},
		{http.MethodGet, "/agents", true},
		{http.MethodPost, "/agents", true},
		{http.MethodGet, "/api/v1/agents", true},
		{http.MethodPost, "/sandbox-profiles", true},
		{http.MethodPost, "/sandbox-profiles/strict", true},
		{http.MethodGet, "/api/v1/settings/sandbox-profiles", true},
		{http.MethodGet, "/automations", true},
		{http.MethodGet, "/api/v1/automations", true},
		{http.MethodGet, "/boards", false},
		{http.MethodPost, "/boards", false},
		{http.MethodGet, "/api/v1/boards", false},
		{http.MethodPost, "/api/v1/boards", false},
		{http.MethodGet, "/account", false},
		{http.MethodGet, "/api/v1/account", false},
		{http.MethodPost, "/api/v1/account/tokens", false},
	}
	roles := []struct {
		name  string
		admin bool
	}{
		{"owner", true},
		{"admin", true},
		{"member", false},
		{"viewer", false},
	}
	for _, ep := range endpoints {
		for _, role := range roles {
			status, message := roleRestriction(ep.method, ep.path, role.name)
			wantDeny := (!role.admin && ep.adminOnly) || (role.name == "viewer" && unsafeMethod(ep.method))
			gotDeny := status == http.StatusForbidden
			if gotDeny != wantDeny {
				t.Errorf("%s %s as %s: status=%d deny=%v, want deny=%v (%s)", ep.method, ep.path, role.name, status, gotDeny, wantDeny, message)
			}
			if !wantDeny && status != 0 {
				t.Errorf("%s %s as %s: status=%d, want allow", ep.method, ep.path, role.name, status)
			}
		}
	}
}

func TestRoleRestrictionStopsAdminMutationsBeforeHandler(t *testing.T) {
	for _, role := range []string{"member", "viewer"} {
		for _, path := range []string{"/api/v1/skills/install", "/api/v1/settings/updates/install", "/sandbox-profiles"} {
			called := false
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if status, message := roleRestriction(r.Method, r.URL.Path, role); status != 0 {
					http.Error(w, message, status)
					return
				}
				next.ServeHTTP(w, r)
			})
			req := httptest.NewRequest(http.MethodPost, path, nil)
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != http.StatusForbidden {
				t.Errorf("%s as %s: status=%d, want 403", path, role, res.Code)
			}
			if called {
				t.Errorf("%s as %s: handler ran after a denied admin mutation", path, role)
			}
		}
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

func TestLoginThrottleSequentialRequests(t *testing.T) {
	throttle := newLoginThrottle()
	at := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	client := "192.0.2.10"
	for attempt := 0; attempt < loginMaxFailures; attempt++ {
		if throttle.blocked(client, at) {
			t.Fatalf("request %d was blocked before the limit", attempt+1)
		}
		throttle.failed(client, at)
	}
	if !throttle.blocked(client, at.Add(time.Minute)) {
		t.Fatal("the request after six sequential failures must be blocked")
	}
	throttle.succeeded(client)
	if throttle.blocked(client, at.Add(time.Minute)) {
		t.Fatal("successful login must clear the temporary block")
	}
}

func TestLoginThrottleExpiredWindowResetsState(t *testing.T) {
	throttle := newLoginThrottle()
	at := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	client := "192.0.2.11"
	for range loginMaxFailures {
		if throttle.blocked(client, at) {
			t.Fatal("request was blocked before the limit")
		}
		throttle.failed(client, at)
	}
	if throttle.blocked(client, at.Add(loginWindow)) {
		t.Fatal("an expired block must allow a new request")
	}
	throttle.failed(client, at.Add(loginWindow))
	if got := throttle.attempts[client].failures; got != 1 {
		t.Fatalf("failures after the window expired = %d, want 1", got)
	}
}

func TestLoginThrottleParallelRequestsReserveLimit(t *testing.T) {
	throttle := newLoginThrottle()
	at := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	client := "192.0.2.12"
	const parallel = 20

	start := make(chan struct{})
	results := make(chan bool, parallel)
	var wg sync.WaitGroup
	for range parallel {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- !throttle.blocked(client, at)
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	allowed := 0
	for admitted := range results {
		if admitted {
			allowed++
			throttle.failed(client, at)
		}
	}
	if allowed != loginMaxFailures {
		t.Fatalf("parallel requests admitted = %d, want %d", allowed, loginMaxFailures)
	}
	if !throttle.blocked(client, at.Add(time.Second)) {
		t.Fatal("parallel failures must activate the block")
	}
}

func TestLoginHandlerBlocksAfterSixSequentialFailures(t *testing.T) {
	user := domain.User{PasswordHash: passwordHash("correct-password")}
	app := &App{
		logins: newLoginThrottle(),
		userLookup: func(context.Context, string) (domain.User, error) {
			return user, nil
		},
	}

	for attempt := 0; attempt < loginMaxFailures; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("email=user%40example.test&password=wrong-password"))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.RemoteAddr = "192.0.2.20:12345"
		response := httptest.NewRecorder()
		app.login(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want %d", attempt+1, response.Code, http.StatusUnauthorized)
		}
	}

	request := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("email=user%40example.test&password=wrong-password"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.RemoteAddr = "192.0.2.20:12345"
	response := httptest.NewRecorder()
	app.login(response, request)
	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status after %d failures = %d, want %d", loginMaxFailures, response.Code, http.StatusTooManyRequests)
	}
	if got, want := response.Header().Get("Retry-After"), strconv.Itoa(int(loginWindow.Seconds())); got != want {
		t.Fatalf("Retry-After = %q, want %q", got, want)
	}
}
