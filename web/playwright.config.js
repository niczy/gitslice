import { defineConfig, devices } from '@playwright/test';

const E2E_GATEWAY_PORT = process.env.E2E_GATEWAY_PORT || '18080';
const E2E_WEB_PORT = process.env.E2E_WEB_PORT || '4173';

export default defineConfig({
  testDir: './tests',
  timeout: 60 * 1000,
  expect: {
    timeout: 10000,
  },
  use: {
    baseURL: `http://127.0.0.1:${E2E_WEB_PORT}`,
    headless: true,
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
    {
      name: 'firefox',
      use: { ...devices['Desktop Firefox'] },
    },
  ],
  webServer: [
    {
      command: 'bash ../scripts/start-e2e-backend.sh',
      port: parseInt(E2E_GATEWAY_PORT, 10),
      reuseExistingServer: !process.env.CI,
      timeout: 120 * 1000,
    },
    {
      command: `AUTH_SECRET=test-auth-secret AUTH_GITHUB_ID=test-github-id AUTH_GITHUB_SECRET=test-github-secret VITE_FILE_API_PROXY_TARGET=http://localhost:${E2E_GATEWAY_PORT} npm run build && HOST=127.0.0.1 PORT=${E2E_WEB_PORT} AUTH_SECRET=test-auth-secret AUTH_GITHUB_ID=test-github-id AUTH_GITHUB_SECRET=test-github-secret VITE_FILE_API_PROXY_TARGET=http://localhost:${E2E_GATEWAY_PORT} npm run start`,
      port: parseInt(E2E_WEB_PORT, 10),
      reuseExistingServer: !process.env.CI,
      timeout: 60 * 1000,
    },
  ],
});
