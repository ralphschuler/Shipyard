# Agent Memory MVP

Technischer Vorschlag für nachvollziehbares, persistentes Agent-Memory in
Postgres. Dieses Dokument grenzt das MVP ab und dient als Implementierungs-
und Review-Grundlage. Es ist keine Freigabe für einen Knowledge Graph und
enthält keinen externen Vector- oder Graph-Speicher.

## Ziel und Leitplanken

Das MVP speichert zwei klar getrennte Arten von Memory:

1. **Conversation Memory**: unveränderliche, paginierbare Nachrichten eines
   Thread-/Task-/Run-Verlaufs.
2. **Facts**: atomare, zeitlich gültige Aussagen mit Quelle, Confidence,
   Status und Versionen.

Jeder Datensatz enthält eine konkrete Provenance-Kette zu Nachricht, Task,
Run und Erstellungszeitpunkt. Jeder Lese- und Schreibpfad wird auf Tenant,
Nutzer, Projekt, Task und Agent begrenzt. Globales oder organisations-
übergreifendes Memory ist nicht Teil des MVP.

Secrets, Tokens, private Schlüssel und hochsensible Inhalte werden vor dem
Persistieren und nochmals vor der Ausgabe erkannt und durch einen stabilen
Redaction-Marker ersetzt. Redaction ist fail-closed: Bei nicht eindeutig
prüfbarem Inhalt wird nicht gespeichert bzw. ausgegeben.

## Datenmodell (Postgres-only)

Die Migrationen sollen die folgenden Tabellen als append-only bzw. historisiert
anlegen. IDs sind UUIDs; Zeitangaben sind `timestamptz` in UTC.

### Gemeinsamer Scope

`memory_scope` wird in Conversation-, Fact-, Audit- und Export-Datensätzen
materialisiert:

| Feld | Bedeutung |
| --- | --- |
| `tenant_id` | Mandant, immer verpflichtend |
| `user_id` | besitzender bzw. berechtigter Nutzer |
| `project_id` | Projektkontext |
| `task_id` | konkreter Task |
| `agent_id` | Agent bzw. Adapter |

Alle Schlüssel sind `NOT NULL`. Autorisierung darf nicht aus einem optionalen
Teil des Scopes abgeleitet werden. Query-Helper akzeptieren ausschließlich
einen bereits validierten `MemoryScope` und erzeugen die vollständige
Scope-Prädikatsklausel serverseitig.

### Conversation Memory

`memory_conversations` enthält pro Nachricht:

- `id`, den vollständigen Scope und `thread_id`;
- `message_id`, `run_id`, `role`, redigierten `content` und `content_hash`;
- `occurred_at`, `created_at`, `expires_at` und optional `deleted_at`;
- `provenance_json`, das die unveränderlichen Ursprungsreferenzen enthält.

Die Tabelle ist logisch append-only. Korrekturen erzeugen einen neuen Eintrag
oder ein Löschereignis; bestehende Nachrichten werden nicht überschrieben.
Ein B-Tree auf `(scope..., thread_id, occurred_at DESC, id DESC)` unterstützt
Keyset-Pagination. Die Volltextsuche verwendet eine `GENERATED`- oder
gespeicherte `tsvector`-Spalte und einen GIN-Index. Suchantworten enthalten
immer Cursor, Quellen und den angewandten Scope.

### Fakten und zeitliche Versionen

`memory_facts` beschreibt die stabile Identität einer Aussage:

- Scope, `subject`, `predicate`, normalisierten `object_json` und
  `dedupe_key`;
- `confidence`, `status` (`pending`, `confirmed`, `revoked`, `superseded`);
- `current_version_id`, `created_at`, `updated_at` und `expires_at`.

`memory_fact_versions` ist unveränderlich und enthält pro Änderung:

- `fact_id`, monoton steigende `version_no` und optional
  `supersedes_version_id`;
- den redigierten Wert, Gültigkeitsintervall (`valid_from`, `valid_until`),
  `confidence` und `change_reason`;
- Provenance zu `message_id`, `task_id`, `run_id`, Agent und Zeitstempel;
- `created_by` und optional `confirmed_by`/`confirmed_at`.

Ein partieller Unique-Index verhindert zwei aktive Versionen derselben
Fact-Identität im selben Scope. Die Deduplizierung erfolgt in einer
Transaktion über `dedupe_key` und einen konfliktfähigen Insert; die
anschließende Versionierung und das Superseding liegen in derselben
Transaktion. Eine neue Version setzt die alte fachlich auf `superseded`, ohne
deren Historie zu verändern.

Facts mit hoher Auswirkung (konfigurierbare Kategorie, z. B. Identität,
Berechtigung oder sicherheitsrelevante Konfiguration) dürfen aus `pending`
nicht in Retrieval-Context-Packs gelangen. Sie werden erst nach expliziter
menschlicher Bestätigung aktiv.

### Audit und Löschung

`memory_audit_events` ist append-only und erfasst mindestens:

`extracted`, `stored`, `read`, `searched`, `confirmed`, `corrected`,
`revoked`, `superseded`, `redacted`, `expired`, `deleted` und `exported`.

Jedes Ereignis trägt Scope, Akteur/Rolle, Ziel-ID, Run-/Request-ID,
Zeitstempel, Ergebnis und eine redigierte Metadatenstruktur. Auditdaten
werden selbst nicht als Memory retrievt.

Physische Löschung wird über eine transaktionale Löschroutine ausgeführt,
die Conversation-Einträge, Facts/Versionen und zugehörige nicht gesetzlich
aufzubewahrende Metadaten entfernt und ein minimales Lösch-Auditereignis
zurücklässt. Exporte verwenden dasselbe Scope-Prädikat wie Retrieval.

## Service- und MCP-Schnittstelle

Der Memory-Service bietet mindestens:

- `appendConversationMessage(scope, provenance, message)`;
- `searchConversation(scope, query, cursor, pageSize)`;
- `upsertFact(scope, fact, provenance)`;
- `confirmFact`, `correctFact`, `revokeFact` und `deleteMemory`;
- `retrieveContextPack(scope, query, tokenBudget, limits)`;
- `exportMemory` und die Retention-Ausführung.

MCP- und HTTP-Handler dürfen keine rohen Store-Methoden direkt exponieren.
Der Service validiert Rolle und vollständigen Scope, redigiert Ein- und
Ausgabe, erzwingt Tokenbudgets und schreibt das Auditereignis. Fehlende oder
widersprüchliche Provenance führt zu einem validierungsbedingten Abbruch.

## Retrieval-Vertrag

`retrieveContextPack` liefert nur:

- Treffer aus exakt dem angeforderten Scope;
- bestätigte Facts bzw. ausdrücklich als niedrigwirksam konfigurierte
  `pending`-Facts;
- paginierte Conversation-Ausschnitte bis zum harten Tokenbudget;
- pro Treffer `source` mit Nachricht-, Task-, Run- und Zeitreferenz;
- `truncated`, verwendetes Budget und eine stabile Retrieval-ID.

Die Auswahl ist deterministisch (Relevanz, Aktualität, Confidence, ID als
Tie-Breaker). Die Tokenzählung erfolgt vor dem Zusammenstellen des Packs; ein
einzelner Treffer darf das Budget nicht überschreiten. Nach dem Pack-Bau
läuft die Redaction nochmals über alle Textfelder.

## UI und Rollen

Der Bereich **Memories** enthält Suche, Scope-Filter, Treffer mit Provenance
und Status sowie Aktionen für Prüfen, Korrigieren, Bestätigen, Widerrufen,
Löschen und Export. Hochwirksame Facts zeigen den Bestätigungsbedarf deutlich
und bleiben bis dahin aus aktivem Retrieval ausgeschlossen.

Rollen werden serverseitig geprüft:

- Leser dürfen im eigenen Scope suchen und Context-Packs abrufen.
- Prüfer dürfen Facts bestätigen, korrigieren und widerrufen.
- Administratoren dürfen Retention und Löschung innerhalb ihres Tenants
  ausführen.

Die UI blendet nicht bloß Buttons aus; jede Aktion muss dieselbe
Autorisierungsprüfung im Service passieren.

## Retention, Metriken und Betrieb

Retention ist pro Tenant/Projekt konfigurierbar und wird auf
`expires_at`/`occurred_at` angewandt. Der Job läuft idempotent in Batches,
verwendet Indizes und protokolliert Anzahl, Dauer und Fehler. Sichere
Löschung und Export werden als explizite, auditierte Operationen behandelt.

Zu messen sind mindestens:

- Retrieval-Latenz, Trefferanzahl und Tokenbudget-Auslastung;
- Scope-Denials und Redaction-Treffer;
- Confirm-/Revoke-/Delete-Rate;
- Retention-Backlog und Löschdauer;
- Evaluationsmetriken für Recall, Präzision und falsche Erinnerungen.

Logs und Metriklabels enthalten keine Memory-Inhalte und keine Secret-Werte.

## Test- und Evaluationsplan

Unit- und Integrationstests decken ab:

1. vollständige Scope-Isolation über Tenant, Nutzer, Projekt, Task und Agent;
2. append-only-Verhalten, Cursor-Pagination und Volltextsuche;
3. Provenance-Pflicht und korrekte Nachricht-/Task-/Run-Verknüpfung;
4. atomare Deduplizierung, Versionsnummern und Superseding;
5. Bestätigung, Widerruf und Ausschluss unbestätigter hochwirksamer Facts;
6. Retention, sichere Löschung und Export innerhalb des Scopes;
7. Secret-/Token-/Private-Key-Redaction bei Speicherung und Ausgabe;
8. harte Tokenbudgets, deterministische Retrieval-Reihenfolge und Audit;
9. API- und UI-Flows für Prüfung, Korrektur und Löschung.

Ein versioniertes Evaluationsset enthält positive Quellen, konkurrierende
Scopes, abgelaufene Facts, absichtlich ähnliche Aussagen und Secret-Fixtures.
Der Evaluationslauf berichtet Recall, Präzision, Budgetverletzungen,
Redaction-Fehler und False-Memory-Rate getrennt pro Scope und Fact-Status.

## Umsetzungsreihenfolge und Abgrenzung

1. Migrationen, Constraints, Indizes und Store-Transaktionen.
2. Redaction, Provenance-Validator, Scope-Authorizer und Audit-Writer.
3. Memory-Service, Retrieval-Vertrag und MCP/HTTP-Adapter.
4. UI, Rollenaktionen und Retention-/Export-Operationen.
5. Tests, Evaluationsset, Metriken und Betriebsdokumentation.

Knowledge Graph, Entitätenkanten, rekursive Traversals, Graph-UI und externe
Vector-Datenbanken bleiben ausdrücklich Phase 3. Dieses MVP nutzt nur
Postgres-Volltextsuche und relationale, zeitliche Fact-Versionen.

## Offene Betriebsentscheidungen vor Implementierungsstart

- konkrete Retention-Werte und gesetzliche Aufbewahrung je Tenant;
- Definition und Liste hochwirksamer Fact-Kategorien;
- verwendete Tokenizer-/Budgetgrenzen;
- Rollenmapping zu den bestehenden Shipyard-Berechtigungen;
- zulässige Audit-Aufbewahrung nach einer Löschung.

Diese Punkte sind Konfiguration bzw. Betriebsfreigabe und ändern nicht die
MVP-Grenze dieses Vorschlags.
