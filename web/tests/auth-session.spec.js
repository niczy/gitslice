// @ts-check
import { test, expect } from '@playwright/test';

test.describe('Cookie-backed web auth', () => {
  test('username login creates a persistent cookie-backed session', async ({ page }) => {
    await page.goto('/login');

    await expect(page.getByTestId('login-page')).toBeVisible();
    await page.getByLabel('Username').fill('webtester1');
    await page.getByRole('button', { name: /login with username/i }).click();

    await expect(page).toHaveURL(/\/browser(\?.*)?$/);
    await expect(page.getByTestId('topbar-profile')).toContainText('webtester1');

    await page.getByTestId('topbar-settings').click();
    await expect(page.getByTestId('settings-page')).toBeVisible();
    await expect(page.getByText(/auth mode/i)).toBeVisible();
    await expect(page.getByText(/^dev$/i)).toBeVisible();

    await page.reload();
    await expect(page.getByTestId('settings-page')).toBeVisible();
    await expect(page.getByTestId('topbar-profile')).toContainText('webtester1');
  });
});
