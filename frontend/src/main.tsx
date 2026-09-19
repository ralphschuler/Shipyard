import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.tsx'
import { normalizeLanguage } from './i18n'

async function bootstrap() {
  const storedLanguage = localStorage.getItem('shipyard-language')
  if (storedLanguage === null) {
    try {
      const response = await fetch('/api/v1/settings/appearance', { credentials: 'same-origin' })
      if (response.ok) {
        const appearance = await response.json() as { Language?: unknown }
        const language = normalizeLanguage(appearance.Language)
        localStorage.setItem('shipyard-language', language)
        document.documentElement.lang = language
      }
    } catch {
      // App-level data loading remains responsible for reporting API failures.
    }
  }

  createRoot(document.getElementById('root')!).render(
    <StrictMode>
      <App />
    </StrictMode>,
  )
}

void bootstrap()
