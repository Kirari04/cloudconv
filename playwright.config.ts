import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './web/e2e',
  timeout: 60_000,
  webServer: {
    command: 'CLOUDCONV_ADDR=:3000 CLOUDCONV_DB_PATH=data/playwright.db CLOUDCONV_UPLOAD_DIR=uploads CLOUDCONV_CONVERTED_DIR=converted CLOUDCONV_SETUP_TOKEN=playwright-token go run .',
    url: 'http://localhost:3000',
    reuseExistingServer: true,
    timeout: 120_000
  },
  use: {
    baseURL: 'http://localhost:3000',
    trace: 'on-first-retry'
  },
  projects: [
    { name: 'chromium', use: { ...devices['Desktop Chrome'] } },
    { name: 'mobile', use: { ...devices['Pixel 7'] } }
  ]
});
