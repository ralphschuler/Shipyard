# To-do-App: E2E-Akzeptanzauftrag

Implementiere eine kleine, lokal ausführbare To-do-Anwendung. Die Anwendung
ist bewusst frameworkfrei, damit der End-to-End-Test sie auf einem frisch
angelegten Workspace zuverlässig ausführen kann. Verwende ausschließlich die
Python-3-Standardbibliothek.

## Auslieferung

Lege diese Artefakte an:

- `todo_app.py` – Domänenlogik und HTTP-Server
- `todo_cli.py` – Kommandozeile für dieselben Kernoperationen
- `index.html` – benutzbare, responsive Oberfläche
- `README.md` – Start, CLI und API dokumentiert
- `tests/test_todo_app.py` – eigene automatisierte Tests

## Fachliches Verhalten

Eine Aufgabe besitzt mindestens `id`, `title`, `description`, `priority`,
`tags`, `status`, `created_at`, `due_date` und `completed_at`.

- Erlaubte Prioritäten: `low`, `normal`, `high`, `urgent`.
- Erlaubte Statuswerte: `open`, `in_progress`, `done`.
- Titel dürfen nicht leer sein; ein Enddatum darf nicht vor dem Startdatum
  liegen.
- Aufgaben sind JSON-persistiert. Ohne Angabe nutzt die Anwendung
  `todos.json` im aktuellen Verzeichnis. Der Pfad kann mit `TODO_DATA_FILE`
  überschrieben werden.
- Suche durchsucht Titel, Beschreibung und Tags ohne Beachtung der
  Groß-/Kleinschreibung.
- Filter können mindestens nach Status, Priorität und Tag angewendet werden.

## CLI

`python3 todo_cli.py` stellt mindestens diese Befehle bereit:

```text
add TITLE [--description TEXT] [--priority PRIORITY] [--tag TAG] [--due YYYY-MM-DD]
list [--status STATUS] [--priority PRIORITY] [--tag TAG] [--query TEXT]
show ID
complete ID
reopen ID
delete ID
```

Mutierende Befehle geben die geänderte Aufgabe als JSON aus. Fehler werden
mit einem Nicht-Null-Exitcode beendet und verständlich auf stderr erklärt.

## HTTP-API und Oberfläche

`python3 todo_app.py serve --port 0` startet einen lokalen HTTP-Server und
gibt die tatsächlich verwendete URL als `LISTENING http://…` aus.

Die API stellt bereit:

- `GET /api/tasks` mit optionalen Filtern `status`, `priority`, `tag`, `q`
- `POST /api/tasks`
- `GET /api/tasks/{id}`
- `PATCH /api/tasks/{id}`
- `POST /api/tasks/{id}/complete`
- `POST /api/tasks/{id}/reopen`
- `DELETE /api/tasks/{id}`
- `GET /healthz` mit `{"status":"ok"}`

`GET /` liefert die Weboberfläche. Sie zeigt einen leeren Zustand, vorhandene
Aufgaben, Filter sowie Erstellen, Abschließen und Wiederöffnen ohne externe
CDNs. Die Oberfläche muss auf schmalen Bildschirmen nutzbar sein.

## Visuelle und zugängliche Qualitätsgrenze

Baue keine generische Formularseite mit einer Reihe identischer Karten. Die
App ist ein persönliches Arbeitswerkzeug: Sie soll eine ruhige,
informationsdichte Arbeitsansicht sein, in der die aktuelle Arbeit zuerst
lesbar wird und Eingaben eine unterstützende Rolle haben.

Lege vor dem Implementieren einen kleinen, konkreten Designplan fest und
spiegele ihn in `README.md` unter **Design**. Er muss enthalten:

- eine Farbpalette aus 4–6 benannten Hex-Farben und eine kurze Begründung;
- Typografie-Rollen für Titel, Fließtext und Metadaten;
- eine einzeilige Layoutbeschreibung für Desktop und Mobil;
- ein prägendes, auf die To-do-App bezogenes Element. Das kann etwa eine
  fokussierte „Heute“-Zusammenfassung oder eine klare Arbeitswarteschlange
  sein – keine dekorative Verlaufsgrafik.

Die Umsetzung folgt diesen verbindlichen Regeln:

- Verwende CSS-Custom-Properties als Token für Farben, Abstände, Radien und
  Fokus. Wiederhole keine willkürlichen Farbwerte über die Regeln hinweg.
- Titel, Status, Priorität, Fälligkeit und Tags haben unterschiedliche,
  lesbare Gewichtungen; Status darf nicht nur über Farbe erkennbar sein.
- Eine Aufgabe ist als klare, scannbare Zeile oder Hierarchie aufgebaut;
  Eingabebereich, Filter und Aufgabenliste dürfen nicht wie drei gleiche
  Karten wirken.
- Die Mobilansicht arbeitet ohne horizontalen Überlauf und mit mindestens
  44px hohen Touch-Zielen. Auf großen Ansichten bleibt die Textbreite
  begrenzt.
- Jeder Eingabewert besitzt ein sichtbares `label` oder einen gleichwertigen
  zugänglichen Namen. Vollständige Bedienung per Tastatur ist möglich;
  `:focus-visible` ist deutlich sichtbar. Statusänderungen werden über eine
  `aria-live`-Region angekündigt.
- Respektiere `prefers-reduced-motion`. Verwende keine externen Schriftarten,
  CDNs oder Bildressourcen.
- Leere Zustände und Fehler erklären die nächste sinnvolle Handlung in
  einfacher Sprache.

Vor dem Abschluss zusätzlich im Browser prüfen: Desktop und eine Breite von
390px, Aufgaben anlegen, filtern, abschließen und wieder öffnen. Es dürfen
keine Netzwerk- oder JavaScript-Fehler in der Browserkonsole stehen.

## Qualitätsgrenze

Vor dem Abschluss ausführen:

```sh
python3 -m unittest discover -s tests -v
python3 verify_todo_app.py
```

Keine virtuelle Umgebung, Abhängigkeiten, Build-Ausgaben oder Testdaten in
den Commit aufnehmen.
