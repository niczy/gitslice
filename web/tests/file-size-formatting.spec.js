import { test, expect } from '@playwright/test';

test('does not show file size before selecting a file', async ({ page }) => {
  await page.goto('/');
  await page.getByTestId('topbar-repo-browser').click();

  await expect(page.locator('.code-header .status')).not.toBeVisible();
});
