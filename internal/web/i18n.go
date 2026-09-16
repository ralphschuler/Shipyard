package web

import "net/http"

// Supported languages are intentionally small and explicit. Keeping the
// validation here gives handlers, cookies and templates one source of truth.
const (
	languageGerman  = "de"
	languageEnglish = "en"
)

func normalizeLanguage(value string) string {
	if value == languageEnglish {
		return languageEnglish
	}
	return languageGerman
}

// resolveLanguage applies the browser preference only when no durable account
// preference is available. The account value is supplied by the auth layer.
func resolveLanguage(accountPreference string, r *http.Request) string {
	if accountPreference == languageGerman || accountPreference == languageEnglish {
		return accountPreference
	}
	return language(r)
}

// translations is also used by tests and future server-rendered templates.
// English is the fallback language for a missing key.
var translations = map[string]map[string]string{
	"en": {
		"nav.overview": "Overview", "nav.projects": "Projects", "nav.boards": "Boards", "nav.agents": "Agents", "nav.automations": "Automations", "nav.skills": "Skills", "nav.runs": "Runs", "nav.audit": "Audit", "nav.settings": "Settings",
		"settings.title": "Settings", "settings.appearance": "Appearance & interaction", "settings.language": "Language", "settings.uiLanguage": "Interface language", "settings.save": "Save settings", "settings.accountSaved": "The selection is saved for your account.",
		"auth.signIn": "Sign in", "auth.welcome": "Welcome aboard", "auth.email": "Email address", "auth.password": "Password", "auth.submit": "Sign in",
		"dashboard.title": "Workflow", "dashboard.boards": "Manage boards", "dashboard.tasks": "Tasks", "dashboard.open": "open", "dashboard.completed": "completed",
		"common.cancel": "Cancel", "common.delete": "Delete", "common.save": "Save",
	},
}

func translate(lang, key, fallback string) string {
	if value := translations[normalizeLanguage(lang)][key]; value != "" {
		return value
	}
	if value := translations[languageEnglish][key]; value != "" {
		return value
	}
	return fallback
}
