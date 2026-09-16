# Secret-Verwaltung

Secrets werden in `secrets` ausschließlich als AES-256-GCM-Ciphertext mit zufälligem Nonce gespeichert. Der Schlüssel wird aus `SHIPYARD_SECRET_KEY` bezogen und darf nur als Deployment-Secret gesetzt werden; er wird nicht in der Datenbank, im Audit oder in Run-Logs gespeichert. Fehlt der Schlüssel, schlagen Speicherung und Entschlüsselung geschlossen fehl.

Ein Secret hat standardmäßig keine Agent-Berechtigung. Nur explizite Einträge in `secret_agents` werden beim Start eines Agent-Runs verwendet. CLI-Agenten erhalten diese Werte als Umgebungsvariablen; der OpenAI-Adapter erhält ausschließlich die zu `provider.secret_env` passende Zuordnung als Request-Credential. Kein Run-Pfad liest Provider-Secrets direkt aus der Service-Umgebung. Zuordnungsänderungen werden bei der nächsten Ermittlung vor dem Run wirksam.

UI und API liefern ausschließlich Name, Beschreibung, Variablenname, Status, Zeitstempel und Agent-Referenzen. Der Wert wird beim Anlegen oder Ersetzen nur über den Request verarbeitet und nie zurückgegeben. Ersetzen reaktiviert ein widerrufenes Secret mit neuem Ciphertext; Widerruf lässt den verschlüsselten Wert liegen, verhindert aber jede weitere Injection. Löschen entfernt Wert, Zuordnungen und Metadaten, während das wertfreie Lösch-Audit erhalten bleibt. Bereits laufende Prozesse behalten ihre Prozessumgebung bis zum Ende; für sofortigen Entzug muss der Run beendet werden.

Audit-Ereignisse sind `secret.created`, `secret.replaced`, `secret.agent_assigned`, `secret.agent_unassigned`, `secret.revoked`, `secret.deleted` und `secret.used`. `secret.used` referenziert das verwendete Secret sowie Agent und Run; alle Ereignisse enthalten niemals Secret-Werte.
