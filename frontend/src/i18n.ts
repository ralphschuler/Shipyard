export type Language = "de" | "en";

const translations: Record<Language, Record<string, string>> = {
  de: {
    overview: "Übersicht", projects: "Projekte", boards: "Boards", agents: "Agents",
    automations: "Automationen", skills: "Skills", runs: "Runs", memory: "Memory", audit: "Audit",
    settings: "Einstellungen", account: "Konto & Zugriff", operations: "Operations", connected: "System verbunden",
    overviewDescription: "Arbeitsfluss, Agentenläufe und Entscheidungen an einem Ort.",
    resourcesDescription: "Verwalte die Ressourcen und Vorgänge deines Agenten-Systems.",
    appearance: "Darstellung & Bedienung", appearanceDescription: "Lege Theme, Sprache und sichtbare Tastaturhinweise fest.",
    display: "Darstellung", systemTheme: "Systemdarstellung verwenden", light: "Hell", dark: "Dunkel",
    keyboard: "Tastatur", shortcutHints: "Hinweise zu Shortcuts anzeigen", save: "Einstellungen speichern",
    language: "Sprache", german: "Deutsch", english: "English", languageDescription: "Wähle die Sprache der Shipyard-Oberfläche.",
    saved: "Einstellungen gespeichert.", navigation: "Hauptnavigation", navigationToggle: "Navigation öffnen oder schließen",
    navigationClose: "Navigation schließen", keyboardControl: "Tastatursteuerung",
    availableBoards: "Verfügbare Boards", allBoards: "Alle Boards", noBoards: "Noch keine Boards",
    updatesCheckTitle: "Update-Prüfung", updatesCheckDescription: "Nur freigegebene GitHub-Releases auf dem Zielbranch werden berücksichtigt.", updatesManualTitle: "Jetzt nach Updates suchen", updatesReadOnly: "Die Prüfung ist schreibgeschützt und installiert nichts.", updatesCheckNow: "Jetzt nach Updates suchen", updatesChecking: "Prüfung läuft …", updatesLastChecked: "Zuletzt geprüft", updatesCurrentVersion: "Laufende Version", updatesStatus: "Vergleichsstatus", updatesUpToDate: "System ist aktuell", updatesAvailable: "Update verfügbar", updatesFailed: "Prüfung fehlgeschlagen", updatesCheckFailed: "Die Update-Prüfung konnte nicht abgeschlossen werden.", notAvailable: "nicht angegeben",
  },
  en: {
    overview: "Overview", projects: "Projects", boards: "Boards", agents: "Agents",
    automations: "Automations", skills: "Skills", runs: "Runs", memory: "Memory", audit: "Audit",
    settings: "Settings", account: "Account & access", operations: "Operations", connected: "System connected",
    overviewDescription: "Workflow, agent runs, and decisions in one place.",
    resourcesDescription: "Manage your agent system's resources and operations.",
    appearance: "Appearance & controls", appearanceDescription: "Choose your theme, language, and visible keyboard hints.",
    display: "Display", systemTheme: "Use system appearance", light: "Light", dark: "Dark",
    keyboard: "Keyboard", shortcutHints: "Show keyboard shortcut hints", save: "Save settings",
    language: "Language", german: "Deutsch", english: "English", languageDescription: "Choose the language of the Shipyard interface.",
    saved: "Settings saved.", navigation: "Main navigation", navigationToggle: "Open or close navigation",
    navigationClose: "Close navigation", keyboardControl: "Keyboard controls",
    availableBoards: "Available boards", allBoards: "All boards", noBoards: "No boards yet",
    updatesCheckTitle: "Update check", updatesCheckDescription: "Only approved GitHub releases on the target branch are considered.", updatesManualTitle: "Check for updates now", updatesReadOnly: "This read-only check does not install anything.", updatesCheckNow: "Check for updates now", updatesChecking: "Checking …", updatesLastChecked: "Last checked", updatesCurrentVersion: "Current version", updatesStatus: "Comparison status", updatesUpToDate: "System is up to date", updatesAvailable: "Update available", updatesFailed: "Check failed", updatesCheckFailed: "The update check could not be completed.", notAvailable: "not available",
  },
};

// Compatibility vocabulary for feature screens that still contain a small
// amount of legacy JSX prose. Only complete text nodes and complete
// presentation attributes are eligible; user-authored content is never
// rewritten as a substring.
export const legacyEnglishPhrases: Record<string, string> = {
  "Control room": "Control room", "Operations": "Operations", "Einträge": "entries", "Noch keine Einträge vorhanden.": "No entries yet.",
  "Löschen": "Delete", "Speichern": "Save", "Bearbeiten": "Edit", "Zurück": "Back", "Beschreibung": "Description",
  "Noch kein Board": "No boards yet", "Noch kein Projekt": "No projects yet", "Noch kein Agent": "No agents yet", "Noch keine Gruppen": "No groups yet",
  "Integrationen": "Integrations", "Quelle hinzufügen": "Add source", "Projektquelle hinzufügen": "Add project source", "Quelle vorbereiten": "Prepare source", "Noch keine Projektquellen.": "No project sources yet.",
  "MCP-Token erstellen": "Create MCP token", "Aktive Tokens": "Active tokens", "Token erzeugen": "Create token", "Token wirklich widerrufen?": "Revoke this token?", "Integration wirklich entfernen?": "Remove this integration?",
  "Agentenrichtlinien": "Agent policies", "Diese Abschnitte umschließen jede Arbeitsanweisung.": "These sections wrap every work instruction.", "Globaler Prefix": "Global prefix", "Globaler Suffix": "Global suffix", "Richtlinien speichern": "Save policies",
  "Neue Workflow-Spalte": "New workflow column", "Spalte anlegen": "Create column", "Spalte erstellen": "Create column", "Spalte speichern": "Save column", "Spalte bearbeiten": "Edit column", "Spalte wählen": "Choose column", "Transition speichern": "Save transition", "Transition wirklich löschen?": "Delete this transition?",
  "Board verwalten": "Manage board", "Aufgabe anlegen": "Create task", "Neue Aufgabe": "New task", "Aufgabe speichern": "Save task", "Aufgaben suchen": "Search tasks", "Titel oder Beschreibung": "Title or description", "Alle Spalten": "All columns", "Alle Prioritäten": "All priorities", "Alle Projekte": "All projects", "Alle Filter zurücksetzen": "Reset all filters", "Keine passenden Aufgaben": "No matching tasks", "Noch keine Tags.": "No labels yet.",
  "Task wirklich löschen?": "Delete this task?", "Aufgabe bearbeiten": "Edit task", "Entscheidung benötigt": "Decision required", "Bitte wählen …": "Choose …", "Kommentar hinzufügen": "Add comment", "Noch keine Workflow-Wechsel.": "No workflow transitions yet.", "Nächster Schritt": "Next step", "Agent starten": "Start agent", "Task an Agent übergeben": "Hand off task to agent", "Agent wählen": "Choose agent", "Übergabe speichern": "Save handoff", "Entscheidung anfordern": "Request decision", "Benötigte Entscheidung": "Decision needed",
  "Automationen": "Automations", "Automation wirklich löschen?": "Delete this automation?", "Wähle zuerst ein Board aus.": "Choose a board first.", "Board wählen": "Choose board", "Auslöser": "Trigger", "Task angelegt": "Task created", "Fälligkeit nähert sich": "Due date is approaching", "Beliebige Spalte": "Any column", "Betroffene Tasks prüfen": "Check affected tasks", "Noch keine Regeln vorhanden.": "No rules yet.",
  "Zeitregel wirklich löschen?": "Delete this schedule?", "Noch keine Zeitregeln.": "No schedules yet.", "Webhook wirklich löschen?": "Delete this webhook?", "Noch keine Webhooks.": "No webhooks yet.",
  "Noch keine Runs": "No runs yet", "Noch keine Skills installiert": "No skills installed yet", "Noch keine Audit-Einträge": "No audit entries yet", "Noch keine Protokolleinträge.": "No log entries yet.", "Dateien": "Files", "Änderungen": "Changes", "Änderungen übernehmen": "Apply changes", "Abbrechen": "Cancel", "Bestätigen und übernehmen": "Confirm and apply",
  "Keine Beschreibung.": "No description.", "Keine geänderten Dateien.": "No changed files.", "Noch keine Gate-Ausgabe.": "No gate output yet.", "unbekannter Provider": "unknown provider", "unbekanntes Modell": "unknown model", "keine": "none",
};

const translatedNodes = new WeakMap<Node, string>();
const translatedAttributes = new WeakMap<Element, Record<string, string | null>>();

const germanSource = (value: string) => /[äöüß]/i.test(value) || /\b(?:bitte|einstellungen|projekt|aufgabe|speichern|löschen|bearbeiten|keine|noch|wähle|spalte|status|prüfung|änderung|fehlgeschlagen|übernahme)\b/i.test(value);
const userAuthoredSelector = "textarea, input, pre, code, [contenteditable='true'], [data-user-content], .markdown-content";

function isUserAuthoredNode(node: Node): boolean {
  return !!node.parentElement?.closest(userAuthoredSelector);
}

export function applyLegacyReactLanguage(language: Language, root: ParentNode = document.body): void {
  const dictionary = language === "en" ? legacyEnglishPhrases : Object.fromEntries(Object.keys(legacyEnglishPhrases).map((key) => [key, key]));
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  const nodes: Node[] = [];
  while (walker.nextNode()) nodes.push(walker.currentNode);
  for (const node of nodes) {
    const original = translatedNodes.get(node) ?? node.nodeValue ?? "";
    translatedNodes.set(node, original);
    if (isUserAuthoredNode(node)) continue;
    const value = original.trim();
    if (!value) continue;
    const replacement = dictionary[value] ?? (language === "en" && germanSource(value) ? "Translation unavailable" : undefined);
    if (replacement) {
      const start = original.indexOf(value);
      node.nodeValue = original.slice(0, start) + replacement + original.slice(start + value.length);
    } else {
      node.nodeValue = original;
    }
  }
  root.querySelectorAll?.("[title],[aria-label],[placeholder]").forEach((element) => {
    const previous = translatedAttributes.get(element) ?? {};
    for (const attribute of ["title", "aria-label", "placeholder"]) {
      const original = previous[attribute] ?? element.getAttribute(attribute);
      previous[attribute] = original;
      if (original && !element.matches(userAuthoredSelector)) {
        const replacement = dictionary[original] ?? (language === "en" && germanSource(original) ? "Translation unavailable" : undefined);
        if (replacement) element.setAttribute(attribute, replacement);
        else element.setAttribute(attribute, original);
      }
    }
    translatedAttributes.set(element, previous);
  });
}

export function normalizeLanguage(value: unknown): Language {
  return value === "en" ? "en" : "de";
}

export function translate(language: Language, key: string): string {
  return translations[language][key] ?? translations.en[key] ?? "Translation unavailable";
}
