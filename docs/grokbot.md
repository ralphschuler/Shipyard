# Grokbot / xAI-Provider

Shipyard enthält ab Migration `051_grokbot_provider.sql` den Provider `grokbot`.
Der Adapter verwendet die xAI-kompatible Responses API und lädt den API-Schlüssel
ausschließlich über die Secret-Zuordnung des jeweiligen Agents.

## Einrichtung

1. Lege unter **Einstellungen → Secrets** ein Secret mit dem Wert von `XAI_API_KEY`
   an und ordne es dem Agent zu.
2. Aktiviere **grokbot** unter **Einstellungen → Provider**.
3. Prüfe `Base URL`, falls ein xAI-kompatibler Gateway verwendet wird. Der Standard
   ist `https://api.x.ai/v1`.
4. Wähle `grokbot`, ein verfügbares Modell und den Effort im Agent-Profil.

Die Provider-Optionen können `models` und `efforts` als JSON-Arrays enthalten, zum
Beispiel `{"models":["grok-4"],"efforts":["low","medium","high"]}`. Damit bleibt
die Modellverfügbarkeit installations- und adapterabhängig.

## Einschränkungen

Der xAI-Adapter nutzt dieselbe serverseitige Worktree-Sandbox, Cancellation,
Timeout- und Usage-Erfassung wie der OpenAI-Responses-Adapter. Preise werden nur
geschätzt, wenn für Provider und Modell ein Eintrag im Preis-Katalog existiert.
Secret-Werte erscheinen weder in Run-Logs noch in UI-Antworten.
