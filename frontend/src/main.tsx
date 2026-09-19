import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { normalizeLanguage } from './i18n'

const appearanceBootstrapTimeoutMs = 1500

async function bootstrap() {
  const storedLanguage = localStorage.getItem('shipyard-language')
  if (storedLanguage === null) {
    const controller = new AbortController()
    const timeout = window.setTimeout(() => controller.abort(), appearanceBootstrapTimeoutMs)
    try {
      const response = await fetch('/api/v1/settings/appearance', {
        credentials: 'same-origin',
        signal: controller.signal,
      })
      if (response.ok) {
        const appearance = await response.json() as { Language?: unknown }
        const language = normalizeLanguage(appearance.Language)
        localStorage.setItem('shipyard-language', language)
        document.documentElement.lang = language
      }
    } catch {
      // The appearance endpoint is optional during bootstrap. Mount promptly
      // when it is unavailable; App-level loading handles the eventual error.
    } finally {
      window.clearTimeout(timeout)
    }
  }

  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <App />
    </StrictMode>,
  )
}

void bootstrap()
