import { test, expect } from '@playwright/test';

test.describe('Commit Diff Page', () => {
  test('diff page is not shown before a commit is selected', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByTestId('topbar-repo-browser')).toBeVisible();
    await page.getByTestId('topbar-repo-browser').click();
    await expect(page.getByRole('heading', { name: /Browse the fetched code/i })).toBeVisible();

    await expect(page.getByTestId('commit-diff-page')).not.toBeVisible();
  });
});
