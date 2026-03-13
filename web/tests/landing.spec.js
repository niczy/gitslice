import { test, expect } from '@playwright/test';

test('renders Git Slice landing content and navigation', async ({ page }) => {
  await page.goto('/');

  await expect(page.getByRole('heading', { level: 1, name: /check out a custom slice in seconds/i })).toBeVisible();
  await expect(page.getByRole('button', { name: /Git Slice/i })).toBeVisible();
  await expect(page.getByTestId('topbar-docs-link')).toBeVisible();
  await expect(page.getByTestId('topbar-github-link')).toBeVisible();
  await expect(page.getByTestId('topbar-get-started')).toBeVisible();
  await page.getByTestId('topbar-docs-link').click();
  await expect(page).toHaveURL(/\/docs$/);
  await expect(page.getByRole('heading', { level: 1, name: /one versioned filesystem, two work surfaces\./i })).toBeVisible();

  await page.goto('/browser');
  await expect(page.getByTestId('slice-dropdown-trigger')).toBeVisible();

  await page.getByRole('button', { name: /Git Slice/i }).click();
  await expect(page.getByRole('heading', { level: 1, name: /check out a custom slice in seconds/i })).toBeVisible();
  await expect(page.getByRole('heading', { name: /make local work the main path when the task deserves a real checkout\./i })).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs slice create ui-refresh apps\/web/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs slice checkout <slice-id>/i }).first()).toBeVisible();
  await expect(page.getByText(/gs changeset create --message "refresh settings page" --files src\/routes\/settings\.tsx/i)).toBeVisible();
  await expect(page.getByText(/gs fs write \/\$USER\/app\/NOTICE\.txt --text "hotfix shipped remotely"/i)).toBeVisible();
  await expect(page.getByText(/gs changeset merge <changeset-id>/i)).toBeVisible();
});
