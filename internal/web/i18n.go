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

// The legacy templates are still intentionally readable in German. These
// entries keep their visible vocabulary in the same central dictionary until
// each template can be migrated to an explicit tr key.
var additionalLegacyPhrases = map[string]string{
	"Shipyard": "Shipyard", "Eigene oder ergänzende Antwort": "Own or additional answer", "Überschreibt oder ergänzt die Auswahl.": "Overrides or supplements the selection.", "Tastatursteuerung": "Keyboard controls", "Zum nächsten oder vorherigen Bedienelement": "Move to the next or previous control", "Eingabe / Leertaste": "Enter / Space", "Link, Button oder Auswahl auslösen": "Activate a link, button, or choice", "Navigation in der Seitenleiste": "Navigate in the sidebar", "Dialog oder mobile Navigation schließen": "Close the dialog or mobile navigation", "Hinweise zu Shortcuts anzeigen": "Show keyboard shortcut hints", "Navigation öffnen": "Open navigation", "Navigation schließen": "Close navigation", "Navigation einklappen": "Collapse navigation", "Navigation ausklappen": "Expand navigation",
	"Braucht Aufmerksamkeit": "Needs attention", "Jetzt handeln": "Act now", "Nur offene Punkte, keine Historie.": "Open items only, no history.", "Blockierte Tasks": "Blocked tasks", "Aktuell fehlgeschlagen · 7 Tage": "Failed recently · 7 days", "Offene Agent-Fragen": "Open agent questions", "Fällig in 24 Stunden": "Due within 24 hours", "Verbindung zum Server fehlgeschlagen. Bitte erneut versuchen.": "Connection to the server failed. Please try again.", "Verbindung testen": "Test connection", "Prüfe …": "Checking …", "Provider ist erreichbar.": "Provider is reachable.", "Provider-Test fehlgeschlagen. Prüfe Adapter, Secret und Server-Log.": "Provider test failed. Check the adapter, secret, and server log.",
	"Vollständige Ausgabe wird geladen …": "Loading complete output …", "Die vollständige Ausgabe konnte nicht geladen werden.": "The complete output could not be loaded.", "Ältere Ausgabe wird geladen …": "Loading older output …", "Ältere Ausgabe erneut laden": "Load older output again", "Ältere Ausgabe konnte nicht geladen werden.": "Older output could not be loaded.", "Ablauf": "Flow", "Patch prüfen": "Review patch", "Der Diff wird nur auf Nachfrage aus dem isolierten Worktree geladen.": "The diff is loaded from the isolated worktree only on request.",
	"Shipyard einrichten": "Set up Shipyard", "Lege das erste Owner-Konto für diesen Arbeitsbereich an.": "Create the first owner account for this workspace.", "Workspace erstellen": "Create workspace", "Mindestens 12 Zeichen.": "At least 12 characters.",
	"Live-Überblick": "Live overview", "Der aktuelle Zustand über Boards, Projekte und Agent-Ausführungen.": "The current state of boards, projects, and agent runs.", "Im System": "In the system", "In Arbeit": "In progress", "Abgeschlossen": "Completed", "Erstellt und abgeschlossen": "Created and completed", "Alle Runs öffnen": "Open all runs", "Tasks im Workflow": "Tasks in workflow", "Nach Board und Status": "By board and status", "Noch keine Tasks im Workflow.": "No tasks in the workflow yet.", "Der Arbeitsfluss beginnt mit einem Board.": "The workflow starts with a board.", "Lege einen Bereich und einen Workflow an. Danach erscheinen Durchsatz, Status und Agent-Ausführungen automatisch hier.": "Create a workspace and workflow. Throughput, status, and agent runs will appear here automatically.", "Erstes Board anlegen": "Create first board", "Noch keine Kostenraten oder API-Runs erfasst.": "No cost rates or API runs recorded yet.", "Gelesen": "Read",
	"Agent-Run": "Agent run", "← Zur Aufgabe": "← Back to task", "Run-Protokoll": "Run log", "Live-Terminal per SSH:": "Live terminal over SSH:", "Auslieferung": "Delivery", "Qualitäts-Gate": "Quality gate", "Laufzeit": "Duration", "Tokens": "Tokens", "Änderungen verwerfen": "Discard changes", "Worktree bereinigen": "Clean worktree", "Run abbrechen": "Cancel run", "Mit aktuellen Einstellungen neu starten": "Restart with current settings", "Diff &amp; Qualitäts-Gate": "Diff &amp; quality gate", "Diff-Übersicht": "Diff overview", "Gate-Ausgabe": "Gate output", "Usage &amp; Kosten": "Usage &amp; costs", "Provider / Modell": "Provider / model", "Service-Tier": "Service tier", "API-Aufrufe": "API calls", "Cache-Input": "Cache input", "Cache-Schreiben": "Cache write", "Reasoning": "Reasoning", "Usage-Status": "Usage status", "Kostenquelle": "Cost source", "Preisversion": "Price version", "Berechnet am": "Calculated at", "Protokoll": "Log", "Es werden jeweils": "Showing", "Einträge angezeigt.": "entries.", "Ältere Ausgabe laden": "Load older output", "Vollständige Ausgabe laden": "Load full output", "Noch keine Ausgabe.": "No output yet.",
	"Quellcode": "Source code", "Repositorys gruppieren, Boards zuordnen und lokal synchron halten.": "Group repositories, assign boards, and keep them synced locally.", "Projekt anlegen": "Create project", "Repository-Katalog": "Repository catalog", "Gruppe:": "Group:", "Board:": "Board:", "Bearbeiten": "Edit", "Projekt bearbeiten": "Edit project", "Git-Repository URL": "Git repository URL", "Standard-Branch": "Default branch", "Lokaler Clone-Pfad": "Local clone path", "Gruppen": "Groups", "Neue Gruppe": "New group", "Projekt löschen": "Delete project", "Noch keine Projekte. Lege das erste Repository an.": "No projects yet. Create the first repository.", "Neue Gruppen bekommen automatisch eine Farbe und verschwinden, wenn kein Projekt sie nutzt.": "New groups receive a color automatically and disappear when no project uses them.",
	"Agentenrichtlinien": "Agent policies", "Diese Abschnitte umschließen jede Agentenanweisung. Plattformregeln bleiben unveränderlich.": "These sections wrap every agent instruction. Platform rules remain immutable.", "Globaler Prefix": "Global prefix", "Globaler Suffix": "Global suffix", "Richtlinien speichern": "Save policies", "Workflow-Editor": "Workflow editor", "+ Spalte": "+ Column", "Transitionen": "Transitions", "Transition bearbeiten": "Edit transition", "Aktionsname": "Action name", "Transition löschen": "Delete transition", "Inhalt anpassen": "Adjust content", "Ansicht zurücksetzen": "Reset view", "Canvas maximieren": "Maximize canvas", "Spalte bearbeiten": "Edit column", "Spaltentyp": "Column type", "Standard": "Default", "Inbox – neue Tasks": "Inbox – new tasks", "Needs action – Rückfrage / Blockade": "Needs action – question / blocked", "Done – erledigt": "Done – completed", "Jeder besondere Typ darf pro Board nur einmal vorkommen.": "Each special type may occur only once per board.", "Spalte löschen": "Delete column", "Spalte anlegen": "Create column", "Spalte erstellen": "Create column", "Transition anlegen": "Create transition",
	"Conversation": "Conversation", "Info": "Info", "Changes": "Changes", "Verlauf": "History", "Kommentare": "Comments", "Entscheidung benötigt": "Decision required", "Bitte wählen …": "Please choose …", "Nach der Antwort": "After the answer", "In Blocked bleiben": "Stay blocked", "Antwort speichern": "Save answer", "Kommentar speichern": "Save comment", "Nächster Schritt": "Next step", "Details": "Details", "Zielbereiche": "Target areas", "Projekte:": "Projects:", "Gruppen:": "Groups:", "Ziele bearbeiten": "Edit targets", "Agent starten": "Start agent", "Ein isolierter Run pro aufgelöstem Repository.": "One isolated run per resolved repository.", "Runs starten": "Start runs", "Beschreibung": "Description", "Metadaten": "Metadata", "Labels": "Labels", "Run-Diffs": "Run diffs", "Akzeptierte und ausstehende Änderungen dieses Tasks.": "Accepted and pending changes for this task.", "Keine Run-Änderungen vorhanden.": "No run changes available.", "Übernahme": "Apply", "Änderungen übernehmen": "Apply changes", "Ziele speichern": "Save targets", "Aufgabe bearbeiten": "Edit task", "Fachgebiet": "Area", "Aufgabe erstellen": "Create task", "Tag anlegen": "Create label", "Farbe": "Color", "Board löschen": "Delete board", "Fällig": "Due",
	"Organisation": "Organization", "Arbeitsbereiche bündeln Aufgaben, Workflows und zugehörige Projekte.": "Workspaces bring together tasks, workflows, and related projects.", "Neues Board": "New board", "Arbeitsbereich": "Workspace", "Board öffnen": "Open board", "Noch kein Board": "No board yet", "Lege einen Arbeitsbereich für dein erstes Projekt an.": "Create a workspace for your first project.", "Wähle eine Vorlage. Der Workflow bleibt danach im Editor vollständig anpassbar.": "Choose a template. The workflow remains fully customizable in the editor.", "Board erstellen": "Create board", "Schließen": "Close", "Abmelden": "Sign out", "Neuer MCP-Token": "New MCP token", "Kopiere ihn jetzt. Er wird nicht erneut angezeigt.": "Copy it now. It will not be shown again.", "MCP-Token erstellen": "Create MCP token", "Tokens geben einem Coding-Agent Zugriff im Namen deines Kontos.": "Tokens give a coding agent access on behalf of your account.", "Token erzeugen": "Generate token", "Aktive Token": "Active tokens", "Widerrufen": "Revoke", "Noch kein MCP-Token.": "No MCP token yet.",
	"Orchestrierung": "Orchestration", "Profile mit klaren Workspaces, Arbeitsrahmen und Skill-Grenzen.": "Profiles with clear workspaces, operating boundaries, and skill limits.", "Agent anlegen": "Create agent", "Pausiert": "Paused", "Max. parallele Runs": "Max. parallel runs", "Agent aktiv": "Agent active", "Erinnerungen und Rollenkontext vor der Arbeitsanweisung.": "Reminders and role context before the work instruction.", "Arbeitsanweisung": "Work instruction", "Abschluss- und Übergabeanweisungen.": "Completion and handoff instructions.", "Profil speichern": "Save profile", "Erlaubte Skills": "Allowed skills", "Skills speichern": "Save skills", "Agent löschen": "Delete agent", "Noch keine Agent-Profile": "No agent profiles yet", "Lege ein Profil an, bevor Automationen Agents starten können.": "Create a profile before automations can start agents.",
	"Provider speichern": "Save provider", "Keine Provider konfiguriert.": "No providers configured.", "Agent-Provider": "Agent provider", "Secrets bleiben als Umgebungsvariablen außerhalb der Datenbank. Optionen werden je Provider validiert gespeichert.": "Secrets remain as environment variables outside the database. Options are validated and saved per provider.", "Provider aktiv": "Provider active", "Zusatzoptionen (JSON)": "Additional options (JSON)", "Integrationen": "Integrations", "Quelle hinzufügen": "Add source", "Entfernen": "Remove", "Noch keine Projektquellen": "No project sources yet", "Projektquelle hinzufügen": "Add project source", "Bezeichnung": "Label", "Eigene Basis-URL (optional)": "Custom base URL (optional)", "Quelle vorbereiten": "Prepare source",
	"Nachvollziehbarkeit": "Traceability", "Audit-Protokoll": "Audit log", "Aktionen aus dem Control Panel und MCP. MCP-Einträge zeigen das verwendete Token, die vollständigen Metadaten bleiben aufklappbar.": "Actions from the control panel and MCP. MCP entries show the token used; full metadata remains expandable.", "Zeitpunkt": "Timestamp", "Aktion": "Action", "Akteur": "Actor", "Ergebnis": "Result", "Ressource": "Resource", "Noch keine protokollierten Aktionen.": "No actions logged yet.", "Neueste Aktionen": "Newest actions", "Ältere Aktionen": "Older actions", "Agent-Orchestrierung": "Agent orchestration", "Alle gestarteten Agent-Ausführungen, inklusive Status, Laufzeit und Ergebnis.": "All started agent runs, including status, duration, and result.", "Gestartet": "Started", "Beendet": "Finished", "Noch keine Agent-Runs.": "No agent runs yet.",
	"Agenten-Bibliothek": "Agent library", "Suche im globalen skills.sh-Katalog und erlaube nur geprüfte Fähigkeiten für deine Agents.": "Search the global skills.sh catalog and allow only reviewed skills for your agents.", "Fähigkeit finden": "Find a skill", "Skills suchen": "Search skills", "Installieren": "Install", "Keine passenden Skills gefunden. Versuche einen allgemeineren Begriff.": "No matching skills found. Try a broader term.", "Installiert": "Installed", "Für Agents verfügbar": "Available to agents", "Noch keine Skills installiert.": "No skills installed yet.", "Details und Prüfungen auf skills.sh": "Details and checks on skills.sh",
	"Zeitregeln": "Schedules", "Prüfe fällige Aufgaben nach einem verlässlichen Intervall.": "Check due tasks at a reliable interval.", "Zeitregel anlegen": "Create schedule", "Ereignisregeln": "Event rules", "Prüft alle": "Checks every", "Noch keine Zeitregeln": "No schedules yet", "Lege eine Regel an, damit ein Agent Aufgaben vor dem Fälligkeitsdatum prüfen kann.": "Create a rule so an agent can check tasks before their due date.", "Erste Zeitregel anlegen": "Create first schedule", "Alle Boards": "All boards", "Fällig innerhalb Stunden": "Due within hours", "Prüfintervall Minuten": "Check interval in minutes", "Zeitregel speichern": "Save schedule", "Webhooks": "Webhooks", "Informiere externe Systeme über Agent-Run-Ergebnisse.": "Notify external systems about agent run results.", "Webhook hinzufügen": "Add webhook", "Noch keine Webhooks": "No webhooks yet", "Webhook speichern": "Save webhook",
}

var dynamicLegacyPhrases = map[string]string{
	"Änderung konnte nicht gespeichert werden.": "The change could not be saved.", "Diff laden": "Load diff", "Diff wird geladen …": "Loading diff …", "Diff aktualisieren": "Refresh diff", "Diff ist für diesen Run nicht verfügbar.": "Diff is not available for this run.", "Keine Änderungen im Worktree.": "No changes in the worktree.", "Änderungen ablehnen": "Reject changes", "Run ablehnen": "Reject run", "Das Feedback wird am Task gespeichert und dem nächsten Agentenversuch als Kontext gegeben.": "The feedback is saved on the task and provided as context to the next agent attempt.", "Was soll der Agent ändern?": "What should the agent change?", "Mit Feedback erneut starten": "Restart with feedback", "Ablehnen": "Reject", "An Agent übergeben": "Hand off to an agent", "Task übergeben": "Hand off task", "Die Übergabe wird als Kommentar gespeichert und ist Teil des nächsten Agent-Kontexts.": "The handoff is saved as a comment and becomes part of the next agent context.", "Übergabehinweis": "Handoff note", "Was wurde bereits geprüft, was ist der nächste Schritt?": "What has already been checked, and what is the next step?", "Folge-Run sofort starten": "Start follow-up run immediately", "Übergabe speichern": "Save handoff", "Entscheidung anfordern": "Request decision", "Die Frage wird am Task festgehalten und auf dem Dashboard hervorgehoben.": "The question is kept on the task and highlighted on the dashboard.", "Benötigte Entscheidung": "Decision needed", "Welche Entscheidung wird benötigt und welche Optionen gibt es?": "What decision is needed, and what options are available?", "Betroffene Tasks prüfen": "Check affected tasks", "Wähle zuerst ein Board aus.": "Choose a board first.", "Vorschau wird geladen …": "Loading preview …", "offene Task(s) würden passen.": "open task(s) would match.", "Keine offenen Tasks passen aktuell.": "No open tasks match currently.", "Vorschau konnte nicht geladen werden.": "Preview could not be loaded.", "Aufgabe verschoben": "Task moved", "Dieser Wechsel ist nicht erlaubt": "This transition is not allowed", "Maximierung verlassen": "Exit maximized view", "Inhalt angepasst": "Content fitted", "Der Run-Status wurde aktualisiert.": "The run status was updated.", "Spalten:": "Columns:",
}

func init() {
	for key, value := range additionalLegacyPhrases {
		legacyPhrases[key] = value
	}
	for key, value := range dynamicLegacyPhrases {
		legacyPhrases[key] = value
	}
}

func legacyDictionary(lang string) map[string]string {
	if normalizeLanguage(lang) == languageEnglish {
		return legacyPhrases
	}
	// The browser needs a German dictionary as well.  Returning identity
	// entries is intentional: dynamically created controls can be switched
	// back from English without keeping a second, divergent phrase list.
	dictionary := make(map[string]string, len(legacyPhrases))
	for german := range legacyPhrases {
		dictionary[german] = german
	}
	return dictionary
}

func legacyDictionaries() map[string]map[string]string {
	return map[string]map[string]string{
		languageGerman:  legacyDictionary(languageGerman),
		languageEnglish: legacyDictionary(languageEnglish),
	}
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
	return localizeTextNodes(html, dictionary)
}

// localizeTextNodes handles prose nested in labels and controls, such as
// <label>Name<input ...>. It deliberately translates only a complete text
// node (after trimming whitespace), never an arbitrary substring. As a
// result a user-created title containing a translated phrase remains intact.
func localizeTextNodes(html string, dictionary map[string]string) string {
	var out strings.Builder
	textStart := 0
	for textStart < len(html) {
		rel := strings.IndexByte(html[textStart:], '<')
		if rel < 0 {
			out.WriteString(translateTextNode(html[textStart:], dictionary))
			break
		}
		textEnd := textStart + rel
		out.WriteString(translateTextNode(html[textStart:textEnd], dictionary))
		tagEnd := strings.IndexByte(html[textEnd:], '>')
		if tagEnd < 0 {
			out.WriteString(html[textEnd:])
			break
		}
		tagEnd += textEnd
		out.WriteString(html[textEnd : tagEnd+1])
		textStart = tagEnd + 1
	}
	return out.String()
}

func translateTextNode(node string, dictionary map[string]string) string {
	trimmed := strings.TrimSpace(node)
	if trimmed == "" {
		return node
	}
	value, ok := dictionary[trimmed]
	if !ok {
		return node
	}
	start := strings.Index(node, trimmed)
	return node[:start] + value + node[start+len(trimmed):]
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
