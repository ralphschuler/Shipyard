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
  rg -q "$required" frontend/src/i18n.ts frontend/src/App.tsx || fail "missing deterministic locale guard: $required"
done

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
