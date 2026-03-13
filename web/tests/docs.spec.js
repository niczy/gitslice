import { expect, test } from '@playwright/test';

test('renders the docs page with navigation and core workflows', async ({ page }) => {
  await page.goto('/docs');

  await expect(page.getByRole('heading', { level: 1, name: /one versioned filesystem, two work surfaces\./i })).toBeVisible();
  await expect(page.getByRole('navigation', { name: /documentation navigation/i })).toBeVisible();
  await expect(page.getByRole('link', { name: /mental model/i })).toBeVisible();
  await expect(page.getByRole('heading', { name: /understand the system before choosing a workflow/i })).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs fs write \/\$USER\/app\/NOTICE\.txt --text "hotfix shipped remotely"/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs slice checkout ui-refresh/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs changeset merge <changeset-id>/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs cache stats --checkouts/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs cache prune/i }).first()).toBeVisible();
  await expect(page.getByText(/uploads and checkouts exchange manifests first and then transfer only missing blocks/i)).toBeVisible();
});
