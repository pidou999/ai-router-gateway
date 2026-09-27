import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    host: true,
    port: 5137,
    allowedHosts: ['.monkeycode-ai.online'],
    proxy: {
      '/v1': {
        target: 'http://localhost:5176',
        changeOrigin: true,
      },
      '/api': {
        target: 'http://localhost:5176',
        changeOrigin: true,
      },
    },
  },
})
