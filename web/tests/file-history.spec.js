import { test, expect } from '@playwright/test';

test.describe('File History', () => {
  test('does not show history controls before a file is selected', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    await expect(page.getByTestId('history-toggle')).not.toBeVisible();
  });

  test('shows a genesis commit item after toggling history', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    const readmeButton = page.getByRole('button', { name: /README\.md/i });
    const loadError = page.getByText('Unable to load entries. Confirm the file gateway is running and the slice exists.');
    const loadResult = await Promise.race([
      readmeButton.waitFor({ state: 'visible', timeout: 10000 }).then(() => 'ready'),
      loadError.waitFor({ state: 'visible', timeout: 10000 }).then(() => 'error'),
    ]);

    if (loadResult === 'error') {
      test.skip(true, 'Genesis repository data not available from the backend.');
    }

    await readmeButton.click();
    await page.getByTestId('history-toggle').click();

    const historyItems = page.getByTestId('history-item');
    await expect(historyItems.first()).toBeVisible();
    await expect(historyItems.first().getByText('Genesis: initialize repository files')).toBeVisible();
  });
});
