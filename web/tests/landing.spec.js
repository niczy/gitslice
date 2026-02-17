import { test, expect } from '@playwright/test';

test('renders Git Slice landing content and navigation', async ({ page }) => {
  await page.goto('/');

  await expect(page.getByRole('heading', { level: 1, name: /scope changes by slice/i })).toBeVisible();
  await expect(page.getByText(/^Git Slice$/)).toBeVisible();
  await expect(page.getByTestId('topbar-github-link')).toBeVisible();
  await expect(page.getByTestId('topbar-repo-browser')).toBeVisible();

  await page.getByTestId('topbar-repo-browser').click();
  await expect(page.getByTestId('slice-dropdown-trigger')).toBeVisible();

  await page.getByRole('button', { name: /Git Slice/i }).click();
  await expect(page.getByRole('heading', { name: /Simple workflow/i })).toBeVisible();
  await expect(page.getByRole('heading', { name: /Three commands/i })).toBeVisible();
});
