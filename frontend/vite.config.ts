import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import path from 'node:path'
import { defineConfig, type Plugin } from 'vite'

function buildInfo(): Plugin {
  return {
    name: 'shipyard-build-info',
    generateBundle() {
      this.emitFile({
        type: 'asset',
        fileName: 'build-info.json',
        source: JSON.stringify({
          version: process.env.TASKBOARD_VERSION ?? process.env.VITE_BUILD_VERSION ?? 'development',
          commit: process.env.TASKBOARD_COMMIT_SHA ?? process.env.VITE_BUILD_COMMIT ?? 'unknown',
          goVersion: process.env.TASKBOARD_GO_VERSION ?? process.env.VITE_BUILD_GO_VERSION ?? '',
        }) + '\n',
      })
    },
  }
}

// https://vite.dev/config/
export default defineConfig({
  base: '/app/',
  plugins: [react(), tailwindcss(), buildInfo()],
  build: {
    outDir: '../internal/web/appdist',
    emptyOutDir: true,
    // The panel is frequently opened on a tablet while agents are producing
    // output. Keep the application shell small and cache heavyweight,
    // stable libraries independently.
    rolldownOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes('node_modules')) return
          if (id.includes('recharts')) return 'charts'
          if (id.includes('lucide-react')) return 'icons'
          if (id.includes('react') || id.includes('scheduler')) return 'react'
          if (id.includes('radix-ui') || id.includes('@radix-ui')) return 'ui'
          return 'vendor'
        },
      },
    },
  },
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
    },
  },
})
