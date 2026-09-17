export type Language = "de" | "en";

const translations: Record<Language, Record<string, string>> = {
  de: {
    overview: "Übersicht", projects: "Projekte", boards: "Boards", agents: "Agents",
    automations: "Automationen", skills: "Skills", runs: "Runs", audit: "Audit",
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
  },
  en: {
    overview: "Overview", projects: "Projects", boards: "Boards", agents: "Agents",
    automations: "Automations", skills: "Skills", runs: "Runs", audit: "Audit",
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
  },
};

export function normalizeLanguage(value: unknown): Language {
  return value === "en" ? "en" : "de";
}

export function translate(language: Language, key: string): string {
  return translations[language][key] ?? translations.en[key] ?? key;
}
