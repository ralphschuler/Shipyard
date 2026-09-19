package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"golang.org/x/crypto/argon2"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"taskboard/internal/domain"
	"time"
)

type userKey struct{}

type statusWriter struct {
	http.ResponseWriter
	status int
}

// htmxRedirectWriter turns ordinary Post/Redirect/Get responses into the
// HTMX redirect contract. Existing handlers retain normal browser redirects.
type htmxRedirectWriter struct{ http.ResponseWriter }

func (w *htmxRedirectWriter) WriteHeader(status int) {
	if status >= http.StatusMultipleChoices && status < http.StatusBadRequest {
		if location := w.Header().Get("Location"); location != "" {
			w.Header().Set("HX-Redirect", location)
			w.Header().Del("Location")
			w.Header().Set("Content-Length", "0")
			w.ResponseWriter.WriteHeader(http.StatusOK)
			return
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func (a *App) HTMX(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("HX-Request") != "true" {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Vary", "HX-Request")
		next.ServeHTTP(&htmxRedirectWriter{ResponseWriter: w}, r)
	})
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *statusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

const loginWindow = 15 * time.Minute

type loginAttempt struct {
	failures int
	until    time.Time
}

// loginThrottle is deliberately bounded and in-memory: it protects the
// password endpoint without persisting client addresses in the product DB.
// Browser SSO never passes through this path.
type loginThrottle struct {
	mu       sync.Mutex
	attempts map[string]loginAttempt
}

func newLoginThrottle() *loginThrottle { return &loginThrottle{attempts: map[string]loginAttempt{}} }

func loginClient(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && host != "127.0.0.1" && host != "::1" {
		return host
	}
	// Nginx is the only trusted proxy that can reach the application listener.
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); forwarded != "" {
		return forwarded
	}
	return r.RemoteAddr
}

func (l *loginThrottle) blocked(client string, at time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.attempts[client]
	if !ok || !a.until.After(at) {
		if ok {
			delete(l.attempts, client)
		}
		return false
	}
	return true
}

func (l *loginThrottle) failed(client string, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.attempts) >= 4096 {
		l.attempts = map[string]loginAttempt{}
	}
	a := l.attempts[client]
	a.failures++
	if a.failures >= 6 {
		a.until = at.Add(loginWindow)
	}
	l.attempts[client] = a
}

func (l *loginThrottle) succeeded(client string) {
	l.mu.Lock()
	delete(l.attempts, client)
	l.mu.Unlock()
}

func unsafeMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func isAdminArea(path string) bool {
	return strings.HasPrefix(path, "/settings/") || strings.HasPrefix(path, "/agents") || strings.HasPrefix(path, "/automations") || strings.HasPrefix(path, "/skills") || strings.HasPrefix(path, "/schedules") || strings.HasPrefix(path, "/webhooks") || strings.HasPrefix(path, "/audit")
}

func sameOrigin(r *http.Request) bool {
	expected := "https://" + r.Host
	if origin := r.Header.Get("Origin"); origin != "" {
		return origin == expected
	}
	referer := r.Referer()
	if referer == "" {
		return false
	}
	u, err := url.Parse(referer)
	return err == nil && u.Scheme+"://"+u.Host == expected
}

// validCSRF requires a per-session, unpredictable value in addition to an
// same-origin request. The browser-facing value is kept in a separate,
// SameSite cookie so ordinary HTML forms and fetch requests can submit it;
// the authoritative value remains in the server-side session record.
func validCSRF(r *http.Request, session domain.Session) bool {
	token := r.Header.Get("X-CSRF-Token")
	if token == "" {
		token = r.FormValue("csrf_token")
	}
	return token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(session.CSRFToken)) == 1
}

func trustedProxyRequest(r *http.Request) bool {
	secret := os.Getenv("TASKBOARD_PROXY_SSO_SECRET")
	email := os.Getenv("TASKBOARD_PROXY_SSO_EMAIL")
	if secret == "" || email == "" || r.Header.Get("X-Taskboard-Proxy-Email") != email {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Taskboard-Proxy-Secret")), []byte(secret)) != 1 {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	return err == nil && (host == "127.0.0.1" || host == "::1")
}

func currentUser(c context.Context) (domain.User, bool) {
	u, ok := c.Value(userKey{}).(domain.User)
	return u, ok
}
func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
func tokenHash(v string) string {
	h := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(h[:])
}
func passwordHash(password string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	hash := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	return base64.RawStdEncoding.EncodeToString(salt) + "." + base64.RawStdEncoding.EncodeToString(hash)
}
func passwordMatches(encoded, password string) bool {
	p := strings.Split(encoded, ".")
	if len(p) != 2 {
		return false
	}
	salt, e := base64.RawStdEncoding.DecodeString(p[0])
	if e != nil {
		return false
	}
	want, e := base64.RawStdEncoding.DecodeString(p[1])
	if e != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}
func (a *App) Protected(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'")
		// Authenticated HTML may contain account data and, immediately after
		// creation, a one-time MCP token. It must never be retained by browser
		// history or an intermediary cache.
		if !strings.HasPrefix(r.URL.Path, "/static/") && r.URL.Path != "/favicon.ico" && r.URL.Path != "/healthz" && r.URL.Path != "/mcp" {
			w.Header().Set("Cache-Control", "no-store")
		}
		if unsafeMethod(r.Method) {
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		}
		if strings.HasPrefix(r.URL.Path, "/static/") || r.URL.Path == "/favicon.ico" || r.URL.Path == "/healthz" || r.URL.Path == "/mcp" {
			next.ServeHTTP(w, r)
			return
		}
		has, err := a.store.HasUsers(r.Context())
		if err != nil {
			http.Error(w, "identity store unavailable", 500)
			return
		}
		if !has {
			if r.URL.Path != "/setup" {
				http.Redirect(w, r, "/setup", 303)
				return
			}
			if unsafeMethod(r.Method) && !sameOrigin(r) {
				http.Error(w, "CSRF validation failed", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/login" {
			if trustedProxyRequest(r) {
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
			if unsafeMethod(r.Method) && !sameOrigin(r) {
				http.Error(w, "CSRF validation failed", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie("taskboard_session")
		if err != nil {
			if trustedProxyRequest(r) {
				if unsafeMethod(r.Method) {
					http.Error(w, "CSRF validation failed", http.StatusForbidden)
					return
				}
				u, lookupErr := a.store.UserByEmail(r.Context(), r.Header.Get("X-Taskboard-Proxy-Email"))
				if lookupErr == nil && u.Active {
					if sessionErr := a.startSession(w, r, u); sessionErr != nil {
						log.Printf("proxy session creation failed: %v", sessionErr)
						http.Error(w, "identity store unavailable", http.StatusInternalServerError)
						return
					}
					if prefs, prefsErr := a.store.UserPreferences(r.Context(), u.ID); prefsErr == nil {
						selected := normalizeLanguage(prefs.Language)
						http.SetCookie(w, &http.Cookie{Name: "shipyard_language", Value: selected, Path: "/", MaxAge: 31536000, Secure: secureCookie(r), SameSite: http.SameSiteLaxMode})
						r.AddCookie(&http.Cookie{Name: "shipyard_language", Value: selected})
					}
					next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, u)))
					return
				}
			}
			if strings.HasPrefix(r.URL.Path, "/api/") {
				apiUnauthorized(w)
				return
			}
			http.Redirect(w, r, "/login", 303)
			return
		}
		u, sess, err := a.store.UserBySession(r.Context(), tokenHash(cookie.Value))
		if err != nil {
			http.SetCookie(w, &http.Cookie{Name: "taskboard_session", Value: "", Path: "/", MaxAge: -1})
			if strings.HasPrefix(r.URL.Path, "/api/") {
				apiUnauthorized(w)
				return
			}
			http.Redirect(w, r, "/login", 303)
			return
		}
		// The account preference is authoritative. Heal a missing or stale
		// readable cookie and make it available to the current request so
		// server-rendered pages use it immediately, including after reload.
		if prefs, prefsErr := a.store.UserPreferences(r.Context(), u.ID); prefsErr == nil {
			selected := normalizeLanguage(prefs.Language)
			http.SetCookie(w, &http.Cookie{Name: "shipyard_language", Value: selected, Path: "/", MaxAge: 31536000, Secure: secureCookie(r), SameSite: http.SameSiteLaxMode})
			r.AddCookie(&http.Cookie{Name: "shipyard_language", Value: selected})
		}
		// A browser may retain the readable CSRF cookie while the server-side
		// session was renewed (for example after the local proxy re-established
		// SSO). Refresh it on safe requests before a form can be submitted.
		if !unsafeMethod(r.Method) {
			csrfCookie, cookieErr := r.Cookie("taskboard_csrf")
			if cookieErr != nil || subtle.ConstantTimeCompare([]byte(csrfCookie.Value), []byte(sess.CSRFToken)) != 1 {
				a.setCSRFCookie(w, sess.CSRFToken)
			}
		}
		if unsafeMethod(r.Method) {
			if !sameOrigin(r) {
				http.Error(w, "CSRF validation failed", 403)
				return
			}
			if !validCSRF(r, sess) {
				http.Error(w, "CSRF validation failed", 403)
				return
			}
		}
		if isAdminArea(r.URL.Path) && u.Role != "owner" && u.Role != "admin" {
			http.Error(w, "Diese Aktion erfordert Administratorrechte.", http.StatusForbidden)
			return
		}
		if unsafeMethod(r.Method) && u.Role == "viewer" {
			http.Error(w, "Diese Rolle darf keine Änderungen vornehmen.", http.StatusForbidden)
			return
		}
		authenticated := r.WithContext(context.WithValue(r.Context(), userKey{}, u))
		if !unsafeMethod(r.Method) {
			next.ServeHTTP(w, authenticated)
			return
		}
		// Every accepted control-panel mutation is recorded centrally, so new
		// handlers cannot silently miss the audit trail. CSRF/auth failures have
		// already returned above and therefore never create misleading entries.
		recorded := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(recorded, authenticated)
		status := recorded.status
		if status == 0 {
			status = http.StatusOK
		}
		_ = a.store.RecordAudit(r.Context(), u.ID, "control_panel."+strings.ToLower(r.Method), "http", r.URL.Path, map[string]string{
			"channel": "control_panel", "method": r.Method, "path": r.URL.Path, "status": strconv.Itoa(status),
		})
	})
}

func apiUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":      "unauthorized",
		"reason":      "Anmeldung erforderlich.",
		"next_action": "Melde dich an und starte die Prüfung erneut.",
	})
}
func (a *App) setup(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		a.render(r, w, "setup.html", nil)
		return
	}
	has, _ := a.store.HasUsers(r.Context())
	if has {
		http.Redirect(w, r, "/login", 303)
		return
	}
	email, name, password := r.FormValue("email"), r.FormValue("name"), r.FormValue("password")
	if len(password) < 12 || strings.TrimSpace(email) == "" || strings.TrimSpace(name) == "" {
		http.Error(w, "Name, E-Mail und ein Passwort mit mindestens 12 Zeichen sind erforderlich.", 400)
		return
	}
	u, err := a.store.CreateOwner(r.Context(), email, name, passwordHash(password))
	if err != nil {
		log.Printf("owner setup failed: %v", err)
		http.Error(w, "Setup wurde bereits abgeschlossen oder die Daten sind ungültig.", 409)
		return
	}
	if err := a.startSession(w, r, u); err != nil {
		log.Printf("owner session creation failed: %v", err)
		http.Error(w, "Sitzung konnte nicht erstellt werden.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", 303)
}
func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		a.render(r, w, "login.html", nil)
		return
	}
	client := loginClient(r)
	if a.logins.blocked(client, now()) {
		w.Header().Set("Retry-After", strconv.Itoa(int(loginWindow.Seconds())))
		http.Error(w, "Zu viele fehlgeschlagene Anmeldeversuche. Bitte später erneut versuchen.", http.StatusTooManyRequests)
		return
	}
	u, err := a.store.UserByEmail(r.Context(), r.FormValue("email"))
	if err != nil || !passwordMatches(u.PasswordHash, r.FormValue("password")) {
		a.logins.failed(client, now())
		http.Error(w, "E-Mail oder Passwort ist nicht korrekt.", 401)
		return
	}
	a.logins.succeeded(client)
	if err := a.startSession(w, r, u); err != nil {
		log.Printf("login session creation failed: %v", err)
		http.Error(w, "Sitzung konnte nicht erstellt werden.", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", 303)
}
func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, e := r.Cookie("taskboard_session"); e == nil {
		_ = a.store.DeleteSession(r.Context(), tokenHash(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: "taskboard_session", Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", 303)
}
func (a *App) startSession(w http.ResponseWriter, r *http.Request, u domain.User) error {
	raw, csrf := randomToken(), randomToken()
	if err := a.store.CreateSession(r.Context(), u.ID, tokenHash(raw), csrf, now().AddDate(0, 0, 14)); err != nil {
		return err
	}
	if err := a.store.MarkLogin(r.Context(), u.ID); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: "taskboard_session", Value: raw, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: 14 * 24 * 3600})
	a.setCSRFCookie(w, csrf)
	return nil
}

// setCSRFCookie exposes only the per-session anti-forgery value. The session
// itself remains HttpOnly. Keeping this in one place also lets Protected heal
// a stale browser cookie after a session was renewed by local proxy SSO.
func (a *App) setCSRFCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{Name: "taskboard_csrf", Value: token, Path: "/", Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: 14 * 24 * 3600})
}
func now() (t time.Time) { return time.Now() }
