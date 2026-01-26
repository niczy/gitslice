import { test, expect } from '@playwright/test';

test('renders Git Slice landing content and navigation', async ({ page }) => {
  await page.goto('/');

  await expect(page.getByRole('heading', { level: 1, name: /slice-based workflows/i })).toBeVisible();
  await expect(page.getByText(/Introducing Git Slice/i)).toBeVisible();
  await expect(page.getByRole('link', { name: /GitHub/i })).toBeVisible();
  await expect(page.getByRole('button', { name: /Repo Browser/i })).toBeVisible();

  await page.getByRole('button', { name: /Repo Browser/i }).click();
  await expect(page.getByRole('heading', { name: /Browse the fetched code/i })).toBeVisible();

  await page.getByRole('button', { name: /Git Slice/i }).click();
  await expect(page.getByRole('heading', { name: /How slices keep changes focused/i })).toBeVisible();
  await expect(page.getByRole('heading', { name: /Go from repo to slice/i })).toBeVisible();

  await expect(page.getByRole('heading', { level: 2, name: /Feature highlights/i })).toBeVisible();
  await expect(page.getByRole('heading', { level: 3, name: 'Speed' })).toBeVisible();
  await expect(page.getByRole('link', { name: /Contact the team/i })).toBeVisible();
});
