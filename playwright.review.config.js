// @ts-check
const { defineConfig, devices } = require('@playwright/test');

module.exports = defineConfig({
  testDir: './review-tests',
  timeout: 30000,
  retries: 1,
  reporter: [['list']],
  use: {
    baseURL: 'http://127.0.0.1:4201',
    trace: 'on-first-retry',
  },
  projects: [
    { name: 'review-chromium', use: { ...devices['Desktop Chrome'] } },
  ],
  webServer: {
    command: 'PORT=4201 go run ./cmd/reviewd',
    url: 'http://127.0.0.1:4201/health',
    reuseExistingServer: true,
    timeout: 30000,
  },
});
