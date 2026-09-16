import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import path from 'node:path'
import { defineConfig } from 'vite'

// https://vite.dev/config/
export default defineConfig({
  base: '/app/',
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
    },
  },
  // The panel is frequently opened on a tablet while agents are producing
  // output.  Keep the application shell small and cache heavyweight, stable
  // libraries independently instead of making every navigation parse one
  // large vendor bundle.
  build: {
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
})
