package web

import (
	"net/http"
	"sort"
	"strings"
)

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
	"de": {
		"nav.overview": "Übersicht", "nav.projects": "Projekte", "nav.boards": "Boards", "nav.agents": "Agents", "nav.automations": "Automationen", "nav.skills": "Skills", "nav.runs": "Runs", "nav.audit": "Audit", "nav.settings": "Einstellungen",
		"settings.title": "Einstellungen", "settings.appearance": "Darstellung & Bedienung", "settings.language": "Sprache", "settings.uiLanguage": "Oberflächensprache", "settings.save": "Einstellungen speichern", "settings.accountSaved": "Die Auswahl wird für dein Benutzerkonto gespeichert.",
		"auth.signIn": "Anmelden", "auth.welcome": "Willkommen an Bord", "auth.email": "E-Mail-Adresse", "auth.password": "Passwort", "auth.submit": "Anmelden",
		"dashboard.title": "Arbeitsfluss", "dashboard.boards": "Boards verwalten", "dashboard.tasks": "Tasks", "dashboard.open": "offen", "dashboard.completed": "erledigt",
		"common.cancel": "Abbrechen", "common.delete": "Löschen", "common.save": "Speichern",
	},
	"en": {
		"nav.overview": "Overview", "nav.projects": "Projects", "nav.boards": "Boards", "nav.agents": "Agents", "nav.automations": "Automations", "nav.skills": "Skills", "nav.runs": "Runs", "nav.audit": "Audit", "nav.settings": "Settings",
		"settings.title": "Settings", "settings.appearance": "Appearance & interaction", "settings.language": "Language", "settings.uiLanguage": "Interface language", "settings.save": "Save settings", "settings.accountSaved": "The selection is saved for your account.",
		"auth.signIn": "Sign in", "auth.welcome": "Welcome aboard", "auth.email": "Email address", "auth.password": "Password", "auth.submit": "Sign in",
		"dashboard.title": "Workflow", "dashboard.boards": "Manage boards", "dashboard.tasks": "Tasks", "dashboard.open": "open", "dashboard.completed": "completed",
		"common.cancel": "Cancel", "common.delete": "Delete", "common.save": "Save",
	},
}

// legacyPhrases is the compatibility vocabulary for the server-rendered
// views. Older templates contain prose instead of translation keys; keeping
// that vocabulary here lets those pages use the same source as the browser
// enhancement while they are incrementally converted to tr calls.
var legacyPhrases = map[string]string{
	"Übersicht": "Overview", "Projekte": "Projects", "Boards": "Boards", "Agents": "Agents", "Automationen": "Automations", "Skills": "Skills", "Runs": "Runs", "Audit": "Audit", "Einstellungen": "Settings",
	"Erscheinungsbild": "Appearance", "Erscheinungsbild & Bedienung": "Appearance & interaction", "Darstellung": "Appearance", "Bedienung": "Interaction", "Sprache": "Language", "Oberflächensprache": "Interface language", "Deutsch": "German", "Englisch": "English", "Einstellungen speichern": "Save settings", "Die Auswahl wird für dein Benutzerkonto gespeichert.": "The selection is saved for your account.",
	"Willkommen an Bord": "Welcome aboard", "Melde dich an, um deinen Shipyard zu öffnen.": "Sign in to open your Shipyard.", "E-Mail-Adresse": "Email address", "Passwort": "Password", "Anmelden": "Sign in", "Abmelden": "Sign out", "Name": "Name", "Beschreibung": "Description", "Speichern": "Save", "Abbrechen": "Cancel", "Löschen": "Delete", "Schließen": "Close",
	"Arbeitsfluss": "Workflow", "Boards verwalten": "Manage boards", "Tasks": "Tasks", "Aufgaben": "Tasks", "offen": "open", "erledigt": "completed", "Systemdarstellung verwenden": "Use system appearance", "Hell": "Light", "Dunkel": "Dark", "Tastatur": "Keyboard", "Hinweise zu Shortcuts anzeigen": "Show keyboard shortcut hints",
	"Neue Aufgabe": "New task", "Aufgabe erstellen": "Create task", "Aufgabe": "Task", "Tag anlegen": "Create label", "Board bearbeiten": "Edit board", "Board löschen": "Delete board", "Workflow-Editor": "Workflow editor", "Neue Spalte": "New column", "Keine Boards vorhanden.": "No boards yet.", "Noch keine Tasks im Workflow.": "No tasks in the workflow yet.", "Dieses Board hat noch keinen Workflow.": "This board has no workflow yet.", "Workflow gestalten": "Design workflow",
	"Priorität": "Priority", "Normal": "Normal", "Niedrig": "Low", "Hoch": "High", "Dringend": "Urgent", "Startdatum": "Start date", "Enddatum": "Due date", "Fällig": "Due", "Spalte": "Column", "Nächster Schritt": "Next step", "Jetzt starten": "Start now", "Run öffnen": "Open run", "Agent-Runs": "Agent runs",
	"Konto & MCP-Tokens": "Account & MCP tokens", "Integrationen": "Integrations", "Provider": "Provider", "Agentenrichtlinien": "Agent policies", "Live-Überblick": "Live overview", "Letzte 14 Tage": "Last 14 days", "Task-Durchsatz": "Task throughput", "Run-Status": "Run status", "Verteilung": "Distribution", "Budget": "Budget", "Agent-Kosten": "Agent costs", "Läuft": "Running", "Erfolgreich": "Succeeded", "Fehlgeschlagen": "Failed", "Abgebrochen": "Cancelled", "Wartet": "Queued",
}

func legacyDictionary(lang string) map[string]string {
	if normalizeLanguage(lang) != languageEnglish {
		return map[string]string{}
	}
	return legacyPhrases
}

func localizeHTML(html, lang string) string {
	dictionary := legacyDictionary(lang)
	if len(dictionary) == 0 {
		return html
	}
	keys := make([]string, 0, len(dictionary))
	for key := range dictionary {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	var replacements []string
	for _, key := range keys {
		replacements = append(replacements, key, dictionary[key])
	}
	return strings.NewReplacer(replacements...).Replace(html)
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
