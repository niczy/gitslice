import { test, expect } from '@playwright/test';

test('renders Git Slice landing content and navigation', async ({ page }) => {
  await page.goto('/');

  await expect(page.getByRole('heading', { level: 1, name: /cloud files first\. local checkout only when you need it\./i })).toBeVisible();
  await expect(page.getByRole('button', { name: /Git Slice/i })).toBeVisible();
  await expect(page.getByTestId('topbar-docs-link')).toBeVisible();
  await expect(page.getByTestId('topbar-github-link')).toBeVisible();
  await expect(page.getByTestId('topbar-get-started')).toBeVisible();

  await page.goto('/browser');
  await expect(page.getByTestId('slice-dropdown-trigger')).toBeVisible();

  await page.getByRole('button', { name: /Git Slice/i }).click();
  await expect(page.getByRole('heading', { level: 1, name: /cloud files first\. local checkout only when you need it\./i })).toBeVisible();
  await expect(page.getByRole('heading', { name: /start with `gs fs`\. drop into a checkout only when local tools are the better interface\./i })).toBeVisible();
  await expect(page.getByText(/gs fs write \/\$USER\/app\/README\.md --text "hello from gitslice"/i).first()).toBeVisible();
  await expect(page.getByText(/gs slice checkout home\.\$USER/i)).toBeVisible();
  await expect(page.getByText(/gs changeset merge <changeset-id>/i)).toBeVisible();
});
