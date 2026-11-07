import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
export default defineConfig({
  plugins: [vue()],
  base: '/app/loliafrp/',
  server: { host: '127.0.0.1', proxy: { '/app/loliafrp/api': 'http://127.0.0.1:18080' } },
  build: { sourcemap: false },
})
