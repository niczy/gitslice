import { test, expect } from '@playwright/test';
import { encode } from '@auth/core/jwt';

const corePort = process.env.E2E_CORE_PORT || process.env.E2E_GATEWAY_PORT || '50151';
const gatewayBaseURL = `http://127.0.0.1:${corePort}`;
const authSecret = 'test-auth-secret';

async function addAuthSessionCookie(context, username) {
  const token = await encode({
    secret: authSecret,
    salt: 'authjs.session-token',
    maxAge: 60 * 60,
    token: {
      sub: username,
      username,
      name: username,
      email: `${username}@example.com`,
    },
  });
  await context.addCookies([
    {
      name: 'authjs.session-token',
      value: token,
      domain: '127.0.0.1',
      path: '/',
      httpOnly: true,
      sameSite: 'Lax',
      secure: false,
    },
  ]);
}

test('shows a sign-in prompt for unauthenticated device approval', async ({ page }) => {
  await page.goto('/auth/device?user_code=ABCD-1234');

  await expect(page.getByTestId('device-user-code')).toHaveValue('ABCD-1234');
  await expect(page.getByTestId('device-sign-in')).toBeVisible();
  await expect(page.getByTestId('device-session-state')).toContainText(/sign in to approve this device/i);
});

test('approves a device authorization with an authenticated OAuth session', async ({ page, request }) => {
  const deviceStartResponse = await request.post(`${gatewayBaseURL}/v1/auth/device/start`, {
    data: {},
  });
  expect(deviceStartResponse.ok()).toBeTruthy();
  const deviceStart = await deviceStartResponse.json();

  await addAuthSessionCookie(page.context(), 'device-page-user');

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
