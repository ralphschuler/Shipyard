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

export function normalizeLanguage(value: unknown): Language {
  return value === "en" ? "en" : "de";
}

export function translate(language: Language, key: string): string {
  return translations[language][key] ?? translations.en[key] ?? key;
}
