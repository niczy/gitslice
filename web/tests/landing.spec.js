import { test, expect } from '@playwright/test';

test('renders Git Slice landing content and navigation', async ({ page }) => {
  await page.goto('/');

  await expect(page.getByRole('heading', { level: 1, name: /ship faster with api-first slices for every change/i })).toBeVisible();
  await expect(page.getByRole('button', { name: /Git Slice/i })).toBeVisible();
  await expect(page.getByTestId('topbar-github-link')).toBeVisible();
  await expect(page.getByTestId('topbar-repo-browser')).toBeVisible();

  await page.getByTestId('topbar-repo-browser').click();
  await expect(page.getByTestId('slice-dropdown-trigger')).toBeVisible();

  await page.getByRole('button', { name: /Git Slice/i }).click();
  await expect(page.getByRole('heading', { level: 1, name: /ship faster with api-first slices for every change/i })).toBeVisible();
  await expect(page.getByRole('heading', { name: /why teams switch to gitslice/i })).toBeVisible();
  await expect(page.getByRole('heading', { name: /simple flow, no branch maze/i })).toBeVisible();
});
