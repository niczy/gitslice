import { test, expect } from '@playwright/test';

const corePort = process.env.E2E_CORE_PORT || process.env.E2E_GATEWAY_PORT || '50151';
const gatewayBaseURL = `http://127.0.0.1:${corePort}`;

async function signInWithUsername(page, username) {
  await page.goto('/login');
  await page.getByLabel('Username').fill(username);
  await page.getByRole('button', { name: /login with username/i }).click();
  await expect(page).toHaveURL(/\/slices(\?.*)?$/);
  await expect(page.getByTestId('topbar-profile')).toContainText(username);
}

test('shows a sign-in prompt for unauthenticated device approval', async ({ page }) => {
  await page.goto('/auth/device?user_code=ABCD-1234');

  await expect(page.getByTestId('device-user-code')).toHaveValue('ABCD-1234');
  await expect(page.getByTestId('device-sign-in')).toBeVisible();
  await expect(page.getByTestId('device-session-state')).toContainText(/sign in to approve this device/i);
});

test('approves a device authorization with an authenticated browser session', async ({ page, request }) => {
  const deviceStartResponse = await request.post(`${gatewayBaseURL}/v1/auth/device/start`, {
    data: {},
  });
  expect(deviceStartResponse.ok()).toBeTruthy();
  const deviceStart = await deviceStartResponse.json();

  await signInWithUsername(page, 'device-page-user');

  await page.goto(`/auth/device?user_code=${encodeURIComponent(deviceStart.userCode)}`);
  await expect(page.getByTestId('device-session-state')).toContainText('device-page-user');

  await page.getByTestId('device-authorize').click();
  await expect(page.getByTestId('device-status')).toContainText(/device approved for device-page-user/i);

  const pollResponse = await request.post(`${gatewayBaseURL}/v1/auth/device/poll`, {
    data: { deviceCode: deviceStart.deviceCode },
  });
  expect(pollResponse.ok()).toBeTruthy();
  const pollPayload = await pollResponse.json();

  expect(pollPayload.status).toBe('DEVICE_AUTHORIZATION_STATUS_APPROVED');
  expect(pollPayload.auth.user.username).toBe('device-page-user');
  expect(pollPayload.auth.accessToken).toBeTruthy();
  expect(pollPayload.auth.refreshToken).toBeTruthy();
});
