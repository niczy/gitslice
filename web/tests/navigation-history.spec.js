import { test, expect } from '@playwright/test';

test('navigation pushes history state and reloads from URL', async ({ page }) => {
  await page.goto('/');

  await page.getByTestId('topbar-repo-browser').click();
  await expect(page).toHaveURL(/\/browser$/);
  await expect(page.getByRole('heading', { name: /Browse the fetched code/i })).toBeVisible();

  await page.getByRole('button', { name: /Git Slice/i }).click();
  await expect(page).toHaveURL(/\/$/);

  await page.goBack();
  await expect(page).toHaveURL(/\/browser$/);
  await expect(page.getByRole('heading', { name: /Browse the fetched code/i })).toBeVisible();

  await page.goto('/diff?commit=abc123def456');
  await expect(page.getByTestId('diff-commit-title')).toContainText('abc123def456');

  await page.reload();
  await expect(page.getByTestId('diff-commit-title')).toContainText('abc123def456');
});
