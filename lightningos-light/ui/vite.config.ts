import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  // Dedupe d3-* so reactflow's d3-zoom and recharts' d3-transition operate on
  // the same d3-selection instance. Without this, reactflow throws
  // "selection.interrupt is not a function" because d3-transition monkey-patches
  // a different selection prototype than the one d3-zoom uses.
  resolve: {
    dedupe: ['d3-selection', 'd3-transition', 'd3-zoom', 'd3-drag', 'd3-dispatch']
  },
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      '/api': {
        target: 'https://127.0.0.1:8443',
        changeOrigin: true,
        secure: false
      }
    }
  }
})
