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
nicht von Agent-Runs verändert. Nach dem lokalen Commit legt Shipyard eine
persistent gespeicherte Repository-Queue an. Diese arbeitet Fetch, Rebase,
`git push --force-with-lease` und die PR-Erstellung/-Wiedererkennung als
wiederanlaufbare Schritte ab. Ein Dienstneustart oder ein temporärer Remote-
Fehler verliert daher keine Delivery; der nächste Queue-Lauf setzt beim
gespeicherten Schritt fort. Branch, Basis-/Head-SHA, Queue-Status und PR-
Metadaten werden am Run gespeichert und angezeigt. Nach der PR-Erstellung bleibt
der Queue-Eintrag im Status `pr_open`; der Provider-Merge wird wiederanlaufbar
überwacht. Erst nach bestätigtem Merge wird der verwaltete Checkout per
Fast-Forward auf `origin/<default-branch>` synchronisiert. Ein Rebase-Konflikt
in diesem Ablauf setzt den Task auf Needs action und protokolliert die
betroffenen Dateien.

Vorübergehende Push-/PR-/Netzwerkfehler bleiben mit Backoff erneut anlaufbar.
Dauerhafte Fehler (fehlende Head-SHA, unbekannte Revision, unsauberer
verwalteter Checkout) und erschöpfte Versuche werden als `failed`
abgeschlossen statt endlos requeued. Stale Queue-Worktrees werden vor der
Branch-Reparatur entfernt; ein sauberer verwalteter Checkout, der nicht auf
dem Default-Branch steht, wird dorthin zurückgeschaltet.
