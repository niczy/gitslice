import { test, expect } from '@playwright/test';

test('renders Git Slice landing content and navigation', async ({ page }) => {
  await page.goto('/');

  await expect(page.getByRole('heading', { level: 1, name: /ship reliable software faster with api-first slices/i })).toBeVisible();
  await expect(page.getByRole('button', { name: /Git Slice/i })).toBeVisible();
  await expect(page.getByTestId('topbar-docs-link')).toBeVisible();
  await expect(page.getByTestId('topbar-github-link')).toBeVisible();
  await expect(page.getByTestId('topbar-get-started')).toBeVisible();

  await page.goto('/browser');
  await expect(page.getByTestId('slice-dropdown-trigger')).toBeVisible();

  await page.getByRole('button', { name: /Git Slice/i }).click();
  await expect(page.getByRole('heading', { level: 1, name: /ship reliable software faster with api-first slices/i })).toBeVisible();
  await expect(page.getByRole('heading', { name: /built for clarity across code and review workflows/i })).toBeVisible();
  await expect(page.getByRole('heading', { name: /simple flow, no branch maze/i })).toBeVisible();
});
