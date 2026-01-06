import { test, expect } from '@playwright/test';

test('renders Git Slice landing content and navigation', async ({ page }) => {
  await page.goto('/');

  await expect(page.getByRole('heading', { level: 1, name: /slice-based workflows/i })).toBeVisible();
  await expect(page.getByText(/Introducing Git Slice/i)).toBeVisible();
  await expect(page.getByRole('link', { name: /Explore the workflow/i })).toBeVisible();
  await expect(page.getByRole('link', { name: /See how it works/i })).toBeVisible();

  await page.getByRole('link', { name: /Explore the workflow/i }).click();
  await expect(page.locator('#features')).toBeVisible();

  await page.getByRole('link', { name: /See how it works/i }).click();
  await expect(page.locator('#overview')).toBeVisible();

  await expect(page.getByRole('heading', { level: 2, name: /Feature highlights/i })).toBeVisible();
  await expect(page.getByRole('heading', { level: 3, name: 'Speed' })).toBeVisible();
  await expect(page.getByRole('link', { name: /Contact the team/i })).toBeVisible();
});
