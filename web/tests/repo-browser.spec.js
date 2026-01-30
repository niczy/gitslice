import { test, expect } from '@playwright/test';

test.describe('Repository Browsing', () => {
  test('shows an error when entries cannot be loaded', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    await expect(page.locator('[data-testid="browse-mode"]')).toHaveValue('root');
    await expect(page.getByText('Unable to load entries. Confirm the file gateway is running and the slice exists.')).toBeVisible();
  });

  test('shows slice prompt before loading entries', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    await page.locator('[data-testid="browse-mode"]').selectOption('slice');
    await expect(page.getByText('Enter a Slice ID to browse files.')).toBeVisible();
  });
});
