# Shipyard UI-System-Audit

Stand: 2026-09-19  
Scope: React-Control-Panel (`frontend`) und serverseitige Legacy-Templates
(`internal/web/templates` + `internal/web/static/app.css`)

## Inventur

| Bereich | Aktuelles Muster | Bewertung | Priorität |
| --- | --- | --- | --- |
| Shell / Sidebar | React-Shell mit eigener Sidebar, Board-Disclosure, mobilem Drawer und unabhängiger Scroll-Fläche | funktional konsistent, Farbrollen waren lokal definiert | P1 |
| Seitenstruktur | Header → Hauptaktion/Übersicht → unterstützende Cards/Listen | gute Leitlinie; einzelne Detailseiten enthalten noch lokale Utility-Kombinationen | P1 |
| Spacing / Radien | shadcn-Utilities plus lokale Werte; zentrale Skala war nur im Legacy-CSS vorhanden | gleiche Abstände waren nicht garantiert | P1 |
| Typografie | Geist Variable im React-Panel, Inter/System im Legacy-Panel | visuell unterscheidbar; Migration bewusst noch offen | P2 |
| Farbe / Theme | Semantic shadcn-Tokens; zusätzlich feste grüne/rote/amber `dark:`-Sonderfälle | konnte Light/Dark bei Status und Diff auseinanderlaufen lassen | P1 |
| Komponenten | Cards, Dialoge, Buttons, Badges, Tabs und Tabellen vorhanden | gemeinsame Primitives werden bereits verwendet | P1 |
| Zustände | Loading, Fehler, Leerzustand, Status-Badges und Attention-Bereich vorhanden | Text ist vorhanden; Statusfarben wurden zentralisiert | P1 |
| Dialoge | shadcn Dialog mit Titel, begrenzter Höhe und internem Scrollbereich | Standard für neue React-Flächen | P1 |
| Tablet | Sidebar bleibt bei 900px sichtbar; Listen und Board-Navigation scrollen horizontal/vertikal | durch E2E abgedeckt | P1 |
| Legacy-Seiten | zentrale `app.css` mit eigener Token-Schicht und Pico-Kompatibilität | Parallelfläche; keine unkontrollierte Mischmigration | P2 |

## Standardisierte Tokens und Regeln

Die React-Fläche definiert ihre Foundations in `frontend/src/index.css`:

- `--space-1` bis `--space-6` für wiederkehrende Dichte und Seitenrhythmus
- `--radius-sm` bis `--radius-lg` für Controls, Listen und Oberflächen
- `--shadow-overlay` mit Light-/Dark-Wert für mobile Drawer und Overlays
- semantische Statusrolle `--success` neben `--destructive`
- Sidebar-Rollen (`--sidebar-*`) ausschließlich aus `--background`, `--card`,
  `--muted`, `--border`, `--primary` und `--accent`

Wiederverwendbare Zustandsklassen (`shipyard-status-dot`,
`shipyard-diff-*`, `shipyard-attention`) ersetzen lokale Farb- und
`dark:`-Sonderfälle. Neue Komponenten sollen weiterhin die vorhandenen
shadcn-Tokens (`bg-card`, `text-muted-foreground`, `border-border`) nutzen;
neue Rohfarben benötigen eine dokumentierte Rolle und beide Theme-Werte.

## Review-Matrix

| Oberfläche | Light Desktop | Dark Desktop | Light Tablet | Dark Tablet |
| --- | --- | --- | --- | --- |
| Sidebar + Boards-Disclosure | E2E: `sidebar.spec.ts` | Theme-Token geprüft | E2E bei 900px | Token-Paar geprüft |
| Board-/Resource-Listen | Header/Card/List-Regel | semantic surfaces | flexible content width | no fixed light surface |
| Dialoge | bounded modal + focus | `bg-card`/`bg-muted` | viewport-bounded | same semantic surfaces |
| Empty/Error/Loading | text plus action/context | same roles | scroll-safe | same roles |

Die bestehende Playwright-Suite ist der reproduzierbare Review-Nachweis für
Sidebar, Drawer-Fokus, Board-Empty-State und Tablet. Für neue Screenshots
sollen die vier Matrix-Varianten als Review-Artefakte ergänzt werden, bevor
ein neuer Screen akzeptiert wird.

## Offene Migrationspunkte

1. Legacy-Templates und React-Routen haben noch zwei bewusst getrennte
   Tokenfamilien; eine Zusammenführung braucht eine Routing-/Rollout-Entscheidung.
2. Einzelne ältere React-Detailbereiche enthalten weiterhin lokale Utilities;
   sie sind funktional, aber Kandidaten für eine nächste Konsolidierungsrunde.
3. Visuelle Pixel-Regressionsbilder sind noch nicht als versionierte Artefakte
   abgelegt; die vorhandenen E2E-Flows sichern Verhalten und Viewport-Nutzung.
