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

// secureCookie follows the connection as seen by the application. Shipyard
// commonly runs behind TLS-terminating nginx, so X-Forwarded-Proto is needed
// there; local HTTP development must remain able to read the preference cookie.
func secureCookie(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
	return strings.EqualFold(forwarded, "https")
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
	"Anmelden · Shipyard": "Sign in · Shipyard",
	"Hauptnavigation":     "Main navigation", "← Boards": "← Boards", "← Zum Board": "← Back to board", "Bearbeiten": "Edit", "Erledigt": "Completed", "Aktiv": "Active", "Verlauf": "History", "Kommentare": "Comments", "Entscheidung benötigt": "Decision required", "Eigene oder ergänzende Antwort": "Own or additional answer", "Überschreibt oder ergänzt die Auswahl.": "Overrides or supplements the selection.", "Nach der Antwort": "After the answer", "Optional: Task direkt weitergeben. Ohne Auswahl bleibt der Task blockiert und derselbe Agent wird fortgesetzt.": "Optional: move the task immediately. Without a selection, the task remains blocked and the same agent continues.", "Antwort speichern": "Save answer", "Kommentar schreiben …": "Write a comment …", "Kommentar": "Comment", "Noch keine Kommentare.": "No comments yet.",
	"Details": "Details", "Start": "Start", "Ende": "End", "Zielbereiche": "Target areas", "keine direkte Auswahl": "no direct selection", "keine": "none", "Ziele bearbeiten": "Edit targets", "Agent starten": "Start agent", "Ein isolierter Run pro aufgelöstem Repository.": "One isolated run per resolved repository.", "Runs starten": "Start runs", "Metadaten": "Metadata", "Status": "Status", "Labels": "Labels", "Keine Labels": "No labels", "Run-Diffs": "Run diffs", "Akzeptierte und ausstehende Änderungen dieses Tasks.": "Accepted and pending changes for this task.", "Kein Diff verfügbar.": "No diff available.", "Keine Run-Änderungen vorhanden.": "No run changes available.", "Übernahme": "Apply", "Nach erfolgreichem Gate kann die Änderung in den verwalteten Checkout übernommen werden.": "After a successful gate, the change can be applied to the managed checkout.", "Änderungen übernehmen": "Apply changes", "Ziele speichern": "Save targets", "Aufgabe bearbeiten": "Edit task", "Änderungen speichern": "Save changes", "Lege zuerst Tags an.": "Create labels first.", "z. B. Frontend": "e.g. Frontend", "Board und alle Tasks wirklich löschen?": "Really delete the board and all tasks?",
	"Projekte zuordnen": "Assign projects", "damit Agents im richtigen Repository arbeiten.": "so agents work in the correct repository.", "Lege zuerst eine Einstiegsspalte an.": "Create an initial column first.", "Danach können Tasks direkt hier erstellt werden.": "After that, tasks can be created here.", "Erstes Board anlegen": "Create first board", "Keine Spalten vorhanden.": "No columns yet.",
	"Agent-Vorlagen": "Agent templates", "Vorlagen": "Templates", "Starte mit einer klaren Rolle.": "Start with a clear role.", "Implementierung": "Implementation", "Code Review": "Code review", "Dokumentation": "Documentation", "Agent aus Vorlage anlegen": "Create agent from template", "Noch nicht konfiguriert": "Not configured yet", "Pausiert": "Paused", "Provider speichern": "Save provider", "Änderungen verwerfen": "Discard changes", "Keine Provider konfiguriert.": "No providers configured.",
	"Audit-Protokoll": "Audit log", "Nachvollziehbarkeit": "Traceability", "Zeitpunkt": "Timestamp", "Aktion": "Action", "Akteur": "Actor", "Ergebnis": "Result", "Ressource": "Resource", "Details anzeigen": "Show details", "anzeigen": "show", "Noch keine protokollierten Aktionen.": "No actions logged yet.", "Ältere Aktionen": "Older actions", "Neueste Aktionen": "Newest actions",
	"Erscheinungsbild · Shipyard": "Appearance · Shipyard", "Lege fest, wie Shipyard dargestellt wird und ob Tastaturhinweise sichtbar sind.": "Choose how Shipyard is displayed and whether keyboard hints are visible.", "Tab, Enter, Leertaste und die Sidebar-Pfeiltasten bleiben immer verfügbar.": "Tab, Enter, Space and sidebar arrow keys are always available.",
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
	// Legacy templates are progressively being converted to translation keys.
	// Until that is complete, only replace complete text nodes and complete
	// presentation attributes. Replacing arbitrary substrings is unsafe: a
	// user may legitimately name a board or task "Neue Aufgabe" and that data
	// must never be translated by the renderer.
	for _, key := range keys {
		value := dictionary[key]
		for _, delimiter := range []struct{ open, close string }{
			{">", "<"},
			{" title=\"", "\""},
			{" aria-label=\"", "\""},
			{" placeholder=\"", "\""},
		} {
			old := delimiter.open + key + delimiter.close
			replacement := delimiter.open + value + delimiter.close
			html = strings.ReplaceAll(html, old, replacement)
		}
	}
	return html
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
