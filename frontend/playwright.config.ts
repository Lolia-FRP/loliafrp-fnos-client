import { defineConfig } from '@playwright/test'
export default defineConfig({
  testDir: './tests',
  fullyParallel: true,
  reporter: 'list',
  use: { baseURL: 'http://127.0.0.1:3189', headless: true, screenshot: 'only-on-failure' },
  webServer: { command: 'npm run dev -- --port 3189 --strictPort', url: 'http://127.0.0.1:3189/app/loliafrp/', reuseExistingServer: false },
})
