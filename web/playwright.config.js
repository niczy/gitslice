import { defineConfig, devices } from '@playwright/test';

const E2E_CORE_PORT = process.env.E2E_CORE_PORT || process.env.E2E_GATEWAY_PORT || '50151';
const E2E_WEB_PORT = process.env.E2E_WEB_PORT || '4173';
const E2E_API_BASE_URL = process.env.E2E_API_BASE_URL || `http://127.0.0.1:${E2E_CORE_PORT}`;

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
      command: `E2E_CORE_PORT=${E2E_CORE_PORT} bash ../scripts/start-e2e-backend.sh`,
      port: parseInt(E2E_CORE_PORT, 10),
      reuseExistingServer: !process.env.CI,
      timeout: 120 * 1000,
    },
    {
      command: `AUTH_SECRET=test-auth-secret ALLOW_DEV_LOGIN=1 PUBLIC_API_BASE_URL=${E2E_API_BASE_URL} VITE_FILE_API_BASE_URL=${E2E_API_BASE_URL} VITE_FILE_API_PROXY_TARGET=${E2E_API_BASE_URL} npm run build && HOST=127.0.0.1 PORT=${E2E_WEB_PORT} AUTH_SECRET=test-auth-secret ALLOW_DEV_LOGIN=1 PUBLIC_API_BASE_URL=${E2E_API_BASE_URL} VITE_FILE_API_BASE_URL=${E2E_API_BASE_URL} VITE_FILE_API_PROXY_TARGET=${E2E_API_BASE_URL} npm run start`,
      port: parseInt(E2E_WEB_PORT, 10),
      reuseExistingServer: !process.env.CI,
      timeout: 60 * 1000,
    },
  ],
});
