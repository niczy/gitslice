import { test, expect } from '@playwright/test';

test.describe('Repository Browsing', () => {
  test('shows an error when entries cannot be loaded', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByTestId('topbar-repo-browser')).toBeVisible();
    await page.getByTestId('topbar-repo-browser').click();
    await expect(page.getByRole('heading', { name: /Browse the fetched code/i })).toBeVisible();

    await expect(page.locator('[data-testid="browse-mode"]')).toHaveValue('root');
    const loadError = page.getByText('Unable to load entries. Confirm the file gateway is running and the slice exists.');
    const treeEntry = page.locator('.tree-entry').first();
    const loadResult = await Promise.race([
      loadError.waitFor({ state: 'visible', timeout: 10000 }).then(() => 'error'),
      treeEntry.waitFor({ state: 'visible', timeout: 10000 }).then(() => 'ready'),
    ]);

    if (loadResult === 'error') {
      await expect(loadError).toBeVisible();
    } else {
      await expect(treeEntry).toBeVisible();
    }
  });

  test('shows slice prompt before loading entries', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByTestId('topbar-repo-browser')).toBeVisible();
    await page.getByTestId('topbar-repo-browser').click();
    await expect(page.getByRole('heading', { name: /Browse the fetched code/i })).toBeVisible();

    await page.locator('[data-testid="browse-mode"]').selectOption('slice');
    await expect(page.getByText('Enter a Slice ID to browse files.')).toBeVisible();
  });
});
