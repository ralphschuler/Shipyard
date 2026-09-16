package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestSecureCookieMatchesDirectAndForwardedHTTPS(t *testing.T) {
	httpRequest := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	if secureCookie(httpRequest) {
		t.Fatal("plain HTTP request must not receive a Secure preference cookie")
	}
	httpsRequest := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	if !secureCookie(httpsRequest) {
		t.Fatal("HTTPS request must receive a Secure preference cookie")
	}
	forwardedHTTPS := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	forwardedHTTPS.Header.Set("X-Forwarded-Proto", "https, http")
	if !secureCookie(forwardedHTTPS) {
		t.Fatal("TLS-terminated HTTPS request must receive a Secure preference cookie")
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

func TestLocalizeHTMLTranslatesLegacyViewAndKeepsGermanDefault(t *testing.T) {
	html := `<html lang="dynamic"><nav aria-label="Hauptnavigation"><a>Übersicht</a></nav><h1>Neue Aufgabe</h1><p>Eigener Task-Titel</p></html>`
	english := localizeHTML(html, "en")
	for _, want := range []string{`lang="dynamic"`, "Overview", "New task", "Eigener Task-Titel"} {
		if !strings.Contains(english, want) {
			t.Fatalf("English HTML missing %q: %s", want, english)
		}
	}
	if got := localizeHTML(html, "de"); got != html {
		t.Fatalf("German HTML changed unexpectedly: %s", got)
	}
}

func TestLocalizeHTMLCoversAuthBoardAndTaskSurfaces(t *testing.T) {
	html := `<html lang="dynamic"><h1>Anmelden · Shipyard</h1><p>Melde dich an, um deinen Shipyard zu öffnen.</p><button>Neue Aufgabe</button><button>Board bearbeiten</button><h2>Nächster Schritt</h2><p>Noch keine Kommentare.</p></html>`
	english := localizeHTML(html, languageEnglish)
	for _, want := range []string{"Sign in · Shipyard", "Sign in to open your Shipyard.", "New task", "Edit board", "Next step", "No comments yet."} {
		if !strings.Contains(english, want) {
			t.Fatalf("English surface missing %q: %s", want, english)
		}
	}
}

func TestLocalizeHTMLIsStableForDynamicUserContent(t *testing.T) {
	html := `<main><h1>Board bearbeiten</h1><p>Ein eigener Titel: Neue Aufgabe</p></main>`
	english := localizeHTML(html, languageEnglish)
	if !strings.Contains(english, "Ein eigener Titel: Neue Aufgabe") {
		t.Fatalf("dynamic user content was changed: %s", english)
	}
	if !strings.Contains(english, "<h1>Edit board</h1>") {
		t.Fatalf("complete visible text node was not translated: %s", english)
	}
}

func TestLocalizeHTMLTranslatesNestedControlLabels(t *testing.T) {
	html := `<form><label>E-Mail-Adresse<input name="email"></label><label>Passwort<input type="password"></label><button>Workspace erstellen</button></form>`
	english := localizeHTML(html, languageEnglish)
	for _, want := range []string{"Email address", "Password", "Create workspace"} {
		if !strings.Contains(english, want) {
			t.Fatalf("nested control missing %q: %s", want, english)
		}
	}
}

func TestLocalizeHTMLLeavesPartialDynamicTextUntouched(t *testing.T) {
	html := `<p>Mein Board: Neue Aufgabe</p><p>Neue Aufgabe</p>`
	english := localizeHTML(html, languageEnglish)
	if !strings.Contains(english, "Mein Board: Neue Aufgabe") {
		t.Fatalf("partial dynamic text was changed: %s", english)
	}
	if !strings.Contains(english, "New task") {
		t.Fatalf("complete static text was not translated: %s", english)
	}
}
