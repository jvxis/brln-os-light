import { defineConfig } from 'vite'

export default defineConfig({
  preview: {
    host: '127.0.0.1',
    port: 4175,
    strictPort: true,
    cors: false,
    headers: {
      'Content-Security-Policy': "default-src 'none'; script-src 'self'; style-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'none'",
      'Cache-Control': 'no-store',
      'Referrer-Policy': 'no-referrer',
      'X-Content-Type-Options': 'nosniff',
    },
  },
})
