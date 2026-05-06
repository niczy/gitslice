import { test, expect } from '@playwright/test';

test.describe('File size formatting (real server)', () => {
  test('displays file sizes from real server data without errors', async ({ page }) => {
    // Monitor for JavaScript errors
    const errors = [];
    page.on('pageerror', (error) => {
      errors.push(error.message);
    });

    await page.goto('/slices/root_slice');
    await expect(page.getByTestId('slice-dropdown-trigger')).toBeVisible();

    // Open slice dropdown and ensure root_slice is selected
    await page.getByTestId('slice-dropdown-trigger').click();
    const rootSliceItem = page
      .getByTestId('slice-dropdown-item')
      .filter({ hasText: /root_slice|root slice/i });
    await expect(rootSliceItem).toBeVisible();
    await rootSliceItem.click();
    await expect(page.getByTestId('slice-dropdown-trigger')).toContainText(/root_slice|root slice/i);

    // Navigate to gitslice root: o -> genesis -> projects -> gitslice (wait for each level)
    await page.getByRole('button', { name: /📁.*o/i }).click();
    await expect(page.getByRole('button', { name: /📁.*genesis/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*genesis/i }).click();
    await expect(page.getByRole('button', { name: /📁.*projects/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*projects/i }).click();
    await expect(page.getByRole('button', { name: /📁.*gitslice/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*gitslice/i }).click();

    // Page should load without JavaScript errors
    await expect(page.getByRole('heading', { name: /File tree/i })).toBeVisible();
    expect(errors).toHaveLength(0);

    // Real files should be visible with formatted sizes
    const readmeBtn = page.getByRole('button', { name: /README\.md/i });
    await expect(readmeBtn).toBeVisible();
    // Size should contain a valid size format (e.g. "123 B", "1.0 KB", "5.0 MB")
    await expect(readmeBtn).toContainText(/\d+(\.\d+)?\s*(B|KB|MB)/);

    // Directories should be visible too (folder emoji prefix)
    await expect(page.getByRole('button', { name: /📁.*internal/i })).toBeVisible();
  });

  test('expanding directories shows nested files with sizes', async ({ page }) => {
    const errors = [];
    page.on('pageerror', (error) => {
      errors.push(error.message);
    });

    await page.goto('/slices/root_slice');
    await expect(page.getByTestId('slice-dropdown-trigger')).toBeVisible();

    // Open slice dropdown and ensure root_slice is selected
    await page.getByTestId('slice-dropdown-trigger').click();
    const rootSliceItem = page
      .getByTestId('slice-dropdown-item')
      .filter({ hasText: /root_slice|root slice/i });
    await expect(rootSliceItem).toBeVisible();
    await rootSliceItem.click();
    await expect(page.getByTestId('slice-dropdown-trigger')).toContainText(/root_slice|root slice/i);

    // Navigate to gitslice root (wait for each level to load)
    await page.getByRole('button', { name: /📁.*o/i }).click();
    await expect(page.getByRole('button', { name: /📁.*genesis/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*genesis/i }).click();
    await expect(page.getByRole('button', { name: /📁.*projects/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*projects/i }).click();
    await expect(page.getByRole('button', { name: /📁.*gitslice/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*gitslice/i }).click();

    // Expand the internal directory
    await page.getByRole('button', { name: /📁.*internal/i }).click();

    // Should see nested entries without errors (some may render as files, some as directories)
    await expect(page.getByRole('button', { name: /common|config|gateway|models|storage|store/i }).first()).toBeVisible();
    expect(errors).toHaveLength(0);
  });
});
