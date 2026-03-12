import { test, expect } from '@playwright/test';

test('renders Git Slice landing content and navigation', async ({ page }) => {
  await page.goto('/');

  await expect(page.getByRole('heading', { level: 1, name: /edit remote files directly from the cli/i })).toBeVisible();
  await expect(page.getByRole('button', { name: /Git Slice/i })).toBeVisible();
  await expect(page.getByTestId('topbar-docs-link')).toBeVisible();
  await expect(page.getByTestId('topbar-github-link')).toBeVisible();
  await expect(page.getByTestId('topbar-get-started')).toBeVisible();

  await page.goto('/browser');
  await expect(page.getByTestId('slice-dropdown-trigger')).toBeVisible();

  await page.getByRole('button', { name: /Git Slice/i }).click();
  await expect(page.getByRole('heading', { level: 1, name: /edit remote files directly from the cli/i })).toBeVisible();
  await expect(page.getByRole('heading', { name: /stay remote until local work is actually faster\./i })).toBeVisible();
  await expect(page.getByText(/gs fs write \/\$USER\/app\/hello\.txt --text "hello from gitslice"/i)).toBeVisible();
  await expect(page.getByText(/gs changeset create --message "update readme" --files README\.md/i)).toBeVisible();
  await expect(page.getByText(/gs changeset merge <changeset-id>/i)).toBeVisible();
});
