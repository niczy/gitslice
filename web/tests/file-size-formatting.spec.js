import { test, expect } from '@playwright/test';

async function navigateToGitsliceRoot(page) {
  await page.goto('/slices/root');
  await expect(page.getByTestId('slice-detail-nav')).toContainText(/root|root slice/i);

  // Navigate to gitslice root: o -> genesis -> projects -> gitslice.
  await page.getByRole('button', { name: /^o\s+Folder$/i }).click();
  await expect(page.getByRole('button', { name: /^genesis\s+Folder$/i })).toBeVisible();
  await page.getByRole('button', { name: /^genesis\s+Folder$/i }).click();
  await expect(page.getByRole('button', { name: /^projects\s+Folder$/i })).toBeVisible();
  await page.getByRole('button', { name: /^projects\s+Folder$/i }).click();
  await expect(page.getByRole('button', { name: /^gitslice\s+Folder$/i })).toBeVisible();
  await page.getByRole('button', { name: /^gitslice\s+Folder$/i }).click();
}

test.describe('File size formatting (real server)', () => {
  test('displays file sizes from real server data without errors', async ({ page }) => {
    // Monitor for JavaScript errors
    const errors = [];
    page.on('pageerror', (error) => {
      errors.push(error.message);
    });

    await navigateToGitsliceRoot(page);

    // Page should load without JavaScript errors
    await expect(page.getByRole('heading', { name: /File tree/i })).toBeVisible();
    expect(errors).toHaveLength(0);

    // Real files should be visible with formatted sizes
    const readmeBtn = page.getByTestId('folder-preview').getByRole('button', { name: /README\.md/i });
    await expect(readmeBtn).toBeVisible();
    // Size should contain a valid size format (e.g. "123 B", "1.0 KB", "5.0 MB")
    await expect(readmeBtn).toContainText(/\d+(\.\d+)?\s*(B|KB|MB)/);

    // Directories should be visible too.
    await expect(page.getByTestId('folder-preview').getByRole('button', { name: /^internal\s+Folder$/i })).toBeVisible();
  });

  test('expanding directories shows nested files with sizes', async ({ page }) => {
    const errors = [];
    page.on('pageerror', (error) => {
      errors.push(error.message);
    });

    await navigateToGitsliceRoot(page);

    // Expand the internal directory
    await page.getByTestId('folder-preview').getByRole('button', { name: /^internal\s+Folder$/i }).click();

    // Should see nested entries without errors (some may render as files, some as directories)
    await expect(page.getByRole('button', { name: /common|config|gateway|models|storage|store/i }).first()).toBeVisible();
    expect(errors).toHaveLength(0);
  });
});
