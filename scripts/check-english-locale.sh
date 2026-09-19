#!/usr/bin/env bash
set -euo pipefail

# English-locale regression audit. This is intentionally source-based so it
# can run in CI without a browser or a database.
failures=0
fail() { printf 'English locale audit: %s\n' "$1" >&2; failures=$((failures + 1)); }

if sed -n '/^  en: {/,/^  },/p' frontend/src/i18n.ts | rg -q '[ÄÖÜäöüß]'; then
  fail 'English catalog contains German characters.'
fi

for required in 'Translation unavailable' 'languageReady' 'shipyard-language' 'applyLegacyReactLanguage' 'legacyEnglishPhrases'; do
  rg -q "$required" frontend/src/i18n.ts frontend/src/App.tsx frontend/src/main.tsx internal/web/static/app.js || fail "missing deterministic locale guard: $required"
done

rg -q 'appearanceBootstrapTimeoutMs|AbortController|controller\.abort' frontend/src/main.tsx || fail 'appearance bootstrap is not bounded and fail-open.'
rg -q 'userAuthoredSelector|germanSource.*Translation unavailable' frontend/src/i18n.ts internal/web/static/app.js || fail 'static UI fallback is not protected from user-authored content.'

system_ui_sources=(frontend/src/App.tsx frontend/src/features internal/web/static/app.js internal/web/templates internal/web/web.go)
if rg -n --glob '*.tsx' --glob '*.ts' --glob '*.js' --glob '*.html' --glob '*.go' '[ÄÖÜäöüß]|\b(?:Bitte|Keine|Noch|Löschen|Speichern|Bearbeiten|Agenten|Projekt|Aufgabe|Verbindung|fehlgeschlagen|wird geladen)\b' "${system_ui_sources[@]}" | rg -v 'i18n|germanSource|legacyEnglishPhrases|legacyPhrases|additionalLegacyPhrases|dynamicLegacyPhrases|translateTextNode|Translation unavailable|German|languageGerman|languageGerman|Test|test'; then
  rg -q 'Translation unavailable' frontend/src/i18n.ts internal/web/i18n.go internal/web/static/app.js || fail 'untranslated system UI candidates have no deterministic English fallback.'
fi

if rg -n 'Projekt-ID|Freitext|Arbeite nur am zugewiesenen Task|Führe die projektspezifischen Tests|Verwende nur diese zugewiesenen Skills|Du bist ein Coding-Agent' internal/automation/worker.go internal/automation/openai.go; then
  fail 'system-authored worker instructions still contain German text.'
fi

rg -q 'Project ID:' internal/automation/worker.go || fail 'worker context lost the English Project ID label.'
rg -q 'Free text:' internal/automation/worker.go || fail 'worker context lost the English Free text label.'
rg -q 'Translation unavailable' internal/web/i18n.go internal/web/static/app.js || fail 'server or legacy runtime has no English fallback.'

if (( failures > 0 )); then
  exit 1
fi
printf 'English locale audit passed.\n'
