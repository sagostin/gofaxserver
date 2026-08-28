import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  plugins: [vue()],
  // Portal lives under /portal so a single Caddy host can split:
  //   /portal/* → portal :8081, everything else → gofaxserver :8080
  base: '/portal/',
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/portal/api': 'http://localhost:8081',
    },
  },
})
