# Git-Integration für parallele Tasks

Shipyard hält den konfigurierten Default-Branch (`main` oder `master`) als
sauberen Synchronisationsanker. Jeder Task erhält zusätzlich eine dauerhafte
Branch `task/<task-id>`. Jeder Agent-Run arbeitet in einem eigenen
`agent/run-<run-id>`-Worktree, der von der Task-Branch abgeleitet wird.

Bei der manuellen Übernahme wird die Task-Branch in einem kurzlebigen
Integrations-Worktree aktualisiert:

1. Der aktuelle Remote-Default-Branch wird gefetcht.
2. Die Task-Branch wird auf diesen Stand rebased.
3. Der Run-Diff wird als neuer Commit auf der Task-Branch gespeichert.
4. Der Run-Worktree und der Integrations-Worktree werden entfernt; die
   Task-Branch bleibt für weitere Review-/QA-Schleifen erhalten.

Ein Konflikt beendet die Übernahme mit betroffenen Dateien und ohne Änderung
am Default-Branch. Wiederholungen erkennen einen bereits vorhandenen
Taskboard-Commit anhand von Run-ID und vollständigem Diff. Der bestehende
Repository-Lock serialisiert nur die kurze Git-Integrationsphase; Agenten
können weiterhin parallel laufen.

Die Task-Branch ist der vorgesehene Push-/PR-Zielpunkt. Der Default-Branch wird
nicht von Agent-Runs verändert. Ein PR sollte mit `--force-with-lease` nach
einem Rebase aktualisiert und anschließend über die normalen GitHub-Checks
gemergt werden.
