# Taskboard

Ein lokales Kanban- und Agent-Orchestrierungsboard mit PostgreSQL, HTMX/Pico CSS, Live-Updates und einem Streamable-HTTP-MCP-Endpunkt.

## Lokaler Start

```sh
export DATABASE_URL='postgres://taskboard:taskboard@localhost:5432/taskboard?sslmode=disable'
export TASKBOARD_ADDR='127.0.0.1:8080'
go run ./cmd/taskboard
```

Beim ersten Aufruf erscheint `/setup`. Dort wird genau ein Owner mit einem Passwort von mindestens 12 Zeichen angelegt. Danach sind alle Web-Ansichten nur noch mit einer serverseitigen Session erreichbar.

## MCP

MCP benötigt einen persönlichen Bearer-Token. Er wird im angemeldeten Konto unter **Konto & MCP-Tokens** einmalig erzeugt und nur in diesem Moment vollständig angezeigt.

```sh
curl -H 'Authorization: Bearer tb_…' \
  -H 'Content-Type: application/json' \
  --data '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' \
  https://codex.local/mcp
```

Tokens werden in der Datenbank nur gehasht gespeichert und können auf der Kontoseite widerrufen werden. Anonyme MCP-Anfragen erhalten `401`.

## Automatischer Login im Heimnetz

Die installierte Nginx-Konfiguration kann Taskboard im festgelegten Heimnetz ohne sichtbares Login öffnen. Nginx setzt eine getrennte Owner-E-Mail plus ein zufälliges SSO-Secret und Taskboard akzeptiert diese Header ausschließlich über die Loopback-Verbindung. Das eigentliche Taskboard-Passwort, die Session und MCP-Tokens werden dabei nicht wiederverwendet.

Für ein weniger vertrauenswürdiges Netzwerk kann vor derselben Brücke zusätzlich HTTP Basic Auth aktiviert werden. Dabei wird **nicht** das Taskboard-Passwort in Nginx, einer URL oder einem Header abgelegt:

1. Nginx prüft HTTP Basic Auth über eine Passwortdatei.
2. Nur Nginx verbindet sich auf `127.0.0.1:8080` und setzt einen separaten, zufälligen Proxy-SSO-Secret sowie die Owner-E-Mail.
3. Taskboard akzeptiert diese Header ausschließlich von Loopback und erstellt seine normale, widerrufbare Session.

Die Vorlage liegt unter `deploy/taskboard-sso.conf.example`. `TASKBOARD_PROXY_SSO_EMAIL` und `TASKBOARD_PROXY_SSO_SECRET` liegen in `/etc/taskboard/taskboard.env`; die Nginx-Kopie liegt root-lesbar unter `/etc/nginx/snippets/taskboard-sso.conf`. MCP verwendet weiterhin ausschließlich eigene Bearer-Tokens und wird nicht per Browser-SSO geöffnet.

## Provider und Agent-Runs

- Codex CLI und Claude CLI werden über **Einstellungen → Provider** aktiviert.
- Codex übernimmt in den Zusatzoptionen `reasoning_effort` (`low`, `medium`,
  `high`, `xhigh`), optional `profile` sowie einfache zusätzliche
  Konfigurationswerte unter `config`, etwa
  `{"reasoning_effort":"high","config":{"features.some_feature":true}}`.
- Ein Run arbeitet in einem eigenen Git-Worktree. Erst nach bestandenem Diff-Gate darf er manuell übernommen werden.
- Eine Automation verschiebt eine Aufgabe nach einem erfolgreichen Run nicht
  sofort in ihre Erfolgs-Spalte: Erst **Änderungen übernehmen** löst die
  Erfolgs-Transition aus. Bei mehreren Repository-Zielen müssen alle
  erfolgreichen Worktrees übernommen sein. Fehlgeschlagene Runs folgen
  weiterhin sofort der konfigurierten Failure-Transition beziehungsweise
  werden als Blockierung am Task kommentiert.
- Der gestartete Agent-Prozess erhält keine vollständige Service-Umgebung: Datenbank- und andere Infrastrukturvariablen werden nicht geerbt. Nur `HOME`, `PATH`, Locale-Werte und das explizit konfigurierte Provider-Secret werden weitergereicht.
- Tool-Befehle des OpenAI-Responses-Adapters benötigen `bubblewrap` (`bwrap`). Sie laufen in einem eigenen Dateisystem-, Prozess- und Netzwerk-Namespace: Nur der zugewiesene Git-Worktree ist schreibbar, Systembibliotheken sind read-only und es gibt keine Netzwerkschnittstelle. Fehlt `bwrap`, lehnt Shipyard OpenAI-Runs vor dem API-Aufruf ab, statt eine schwächere Isolation zu verwenden.
- Der Host-Preflight `./deploy/bubblewrap-healthcheck.sh` startet diese Sandbox ohne Worktree- oder Datenbankänderung. Er läuft vor OpenAI-Runs (mit `TASKBOARD_BWRAP_PREFLIGHT=0` abschaltbar) und im Produktionscheck. Die systemd-Unit muss `AF_NETLINK` zusätzlich zu `AF_UNIX AF_INET AF_INET6` erlauben; diese Freigabe gilt nur für `taskboard.service`, weil Bubblewrap beim Aufbau des privaten Netzwerks `NETLINK_ROUTE` benötigt. Nach einer Unit-Änderung: `systemctl daemon-reload && systemctl restart taskboard.service`.
- Der OpenAI-Responses-Adapter nutzt die offizielle Responses-API mit `store:false`. Er kann im zugewiesenen Git-Worktree über ein dokumentiertes Kommando-Werkzeug arbeiten; Modell, API-Key-Umgebungsvariable und optionale Base-URL werden in den Provider-Einstellungen gesetzt. Die API meldet echte Token-Nutzung zurück, die bei einem Run gespeichert wird.

## Integrationen

Unter **Einstellungen → Integrationen** lassen sich GitHub-, GitLab- und Codeberg-Quellen pro Benutzer vorbereiten. Das Datenmodell reserviert Felder für verschlüsselte Credentials, Repository-Quellen und idempotente Webhook-Deliveries. OAuth/GitHub-App-Registrierung, Repository-Auswahl und Import brauchen jedoch Provider-Client-Credentials und werden bewusst nicht durch frei eingegebene Tokens ersetzt.

## Prüfen und Deployen

```sh
go test ./...
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
./deploy/deploy-local.sh
```

### Produktionsnahe Automation-Integrationstests

Die PostgreSQL-Integrationstests sind absichtlich opt-in. Für einen
reproduzierbaren Host- oder CI-Lauf mit echten Listenern und einer getrennten,
temporären Datenbank steht der Podman-Runner bereit:

```sh
timeout 120s ./scripts/run-integration-tests.sh
```

Der Runner startet `docker.io/library/postgres:16-alpine` mit einem zufälligen Loopback-Port und
der ausschließlich für Tests vorgesehenen Datenbank
`taskboard_agent_tests`. Er überschreibt nur `SHIPYARD_TEST_DATABASE_URL`;
`DATABASE_URL` wird nicht verwendet. Die Migrationen werden von den Tests
ausgeführt, und das automatische Cleanup entfernt Container und temporäre
Go-Artefakte auch bei einem Abbruch. Das ist der Rollback: Es bleiben weder
Testdaten noch ein laufender Dienst zurück. Produktionsdaten werden nicht
berührt.

Der Runner muss außerhalb einer restriktiven Delivery-Sandbox bzw. in einem
CI-Runner mit erlaubten Loopback-Listenern und Podman-Netzwerk laufen. Ein
direktes `go test ./...` in der Delivery-Sandbox bleibt deshalb eine explizite
Umgebungsausnahme, wenn Socket-Listener oder localhost-Verbindungen dort
verboten sind.

Das Deploy-Skript kompiliert vor dem Stoppen des Dienstes, behält die vorherige
Binärdatei und stellt sie bei einem fehlgeschlagenen Start oder Health-Check
automatisch wieder her. Ein direktes Überschreiben von `./taskboard` während
der Dienst läuft ist nicht zulässig, weil Linux dies mit `Text file busy`
ablehnen kann.

`deploy/e2e-reset.sh` legt ausschließlich einen frischen, isolierten Git-Workspace für den Todo-End-to-End-Test an. Neben dem leeren Initial-Commit enthält er den versionierten Akzeptanzauftrag `TODO_E2E_SPEC.md` und die unabhängige Black-box-Prüfung `verify_todo_app.py`. Ein Agent muss darin eine echte To-do-App mit CLI, JSON-API, Weboberfläche, Persistenz, Filtern und eigenen Tests liefern. Der Auftrag verlangt außerdem einen dokumentierten, eigenständigen Designplan, responsive Touch-Ziele, sichtbaren Tastaturfokus, reduzierte Bewegung und einen Browsercheck ohne Konsolenfehler. Er gehört nicht zur Produktoberfläche; Workspace, Daten und Token eines Laufs werden anschließend entfernt.

## Backups und Restore-Drill

`deploy/backup-postgres.sh` erzeugt atomare PostgreSQL-Archive mit SHA-256-Manifest. `deploy/verify-production.sh` prüft Alter, Prüfsumme und Lesbarkeit des jüngsten Archivs bei jedem lokalen Deployment und führt standardmäßig den Bubblewrap-Preflight aus. Für eine Installation ohne OpenAI kann er mit `TASKBOARD_VERIFY_BWRAP=0` übersprungen werden. Ein manueller Regressionstest ist `./deploy/bubblewrap-healthcheck.sh`; ein Fehler mit `NETLINK_ROUTE` verweist direkt auf die begrenzte `AF_NETLINK`-Freigabe in `deploy/taskboard.service`.

Ein echter Restore wird bewusst nur gegen eine vorher angelegte, **leere** Testdatenbank ausgeführt. Das Drill-Skript lehnt die Live-Datenbank und jedes Ziel mit vorhandenen Tabellen ab:

```sh
DATABASE_URL='postgres://…/taskboard?sslmode=disable' \
TASKBOARD_RESTORE_DATABASE='postgres://…/taskboard_restore_drill?sslmode=disable' \
bash ./deploy/verify-backup-restore.sh
```

Der Restore-Drill löscht oder erstellt keine Datenbank. Die Bereitstellung und spätere Entsorgung der isolierten Testdatenbank bleibt eine explizite Betriebsaufgabe.
