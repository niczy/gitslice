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
    await expect(page.getByTestId('slice-dropdown-trigger')).toContainText(/webtester1/i);
    await expect(page.getByRole('button', { name: /\+ Folder/i })).toHaveCount(0);
    await expect(page.getByRole('button', { name: /\+ File/i })).toHaveCount(0);

    await page.getByTestId('slice-dropdown-trigger').click();
    await expect(page.getByTestId('slice-dropdown-item').filter({ hasText: /root_slice|root slice/i })).toHaveCount(0);
    await page.keyboard.press('Escape');

    await page.getByTestId('topbar-settings').click();
    await expect(page.getByTestId('settings-page')).toBeVisible();
    await expect(page.getByText(/auth mode/i)).toBeVisible();
    await expect(page.getByText(/^dev$/i)).toBeVisible();

    await page.getByTestId('topbar-repos').click();
    await expect(page).toHaveURL(/\/browser(\?.*)?$/);
    await expect(page.getByTestId('slice-dropdown-trigger')).toContainText(/webtester1/i);

    await page.reload();
    await expect(page.getByTestId('slice-dropdown-trigger')).toBeVisible();
    await expect(page.getByTestId('topbar-profile')).toContainText('webtester1');
  });

  test('stale username-style browser slice param resolves to the home slice', async ({ page }) => {
    await page.goto('/login');
    await page.getByLabel('Username').fill('webtester2');
    await page.getByRole('button', { name: /login with username/i }).click();

    await expect(page).toHaveURL(/\/browser(\?.*)?$/);

    await page.goto('/browser?slice=webtester2');

    await expect(page.getByTestId('slice-dropdown-trigger')).toContainText(/webtester2/i);
    await expect(page.getByText(/Unable to load entries/i)).toHaveCount(0);
    await expect(page.getByRole('button', { name: /📁.*\/webtester2$/i })).toBeVisible();
  });

  test('settings shows repo bindings for the signed-in user', async ({ page }) => {
    const username = `webbindings${Date.now()}`;

    await page.goto('/login');
    await page.getByLabel('Username').fill(username);
    await page.getByRole('button', { name: /login with username/i }).click();
    await expect(page).toHaveURL(/\/browser(\?.*)?$/);

    await page.route('**/v1/repos/bindings', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          bindings: [
            {
              binding_id: 'binding-1',
              path: `/${username}/imports/demo`,
              repo_url: 'https://github.com/example/demo.git',
              branch: 'main',
              push_enabled: true,
              last_imported_commit: 'abc123',
              last_pushed_commit: 'def456',
            },
          ],
        }),
      });
    });

    await page.getByTestId('topbar-settings').click();
    await expect(page.getByTestId('settings-page')).toBeVisible();
    await expect(page.getByRole('heading', { name: /github bindings/i })).toBeVisible();
    await expect(page.getByTestId('settings-repo-bindings')).toContainText(`/${username}/imports/demo`);
    await expect(page.getByTestId('settings-repo-bindings')).toContainText('https://github.com/example/demo.git');
    await expect(page.getByTestId('settings-repo-bindings')).toContainText(/yes/i);
  });
});
