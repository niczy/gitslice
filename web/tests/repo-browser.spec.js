import { test, expect } from '@playwright/test';

// Genesis populates files under genesis/
// so root entries should include the "genesis" directory.

test.describe('Root Repository Browsing (real server)', () => {
  test('loads root entries and shows genesis directory tree', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    await expect(page.getByTestId('slice-list')).toBeVisible();
    await page.getByRole('button', { name: /root_slice/i }).click();

    // The genesis files live under "genesis" at the root level
    await expect(page.getByRole('button', { name: /📁.*genesis/i })).toBeVisible();
  });

  test('navigates into genesis directory and finds repo files', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();
    await page.getByRole('button', { name: /root_slice/i }).click();

    // Navigate: genesis -> repo files
    await page.getByRole('button', { name: /📁.*genesis/i }).click();

    // Should see real repo files
    await expect(page.getByRole('button', { name: /README\.md/i })).toBeVisible();
    await expect(page.getByRole('button', { name: /go\.mod/i })).toBeVisible();
    await expect(page.getByRole('button', { name: /Makefile/i })).toBeVisible();
  });

  test('previews a real file from the genesis repository', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();
    await page.getByRole('button', { name: /root_slice/i }).click();

    // Navigate to genesis root
    await page.getByRole('button', { name: /📁.*genesis/i }).click();

    // Click README.md to preview it
    await page.getByRole('button', { name: /README\.md/i }).click();

    // The file heading should show the path
    await expect(page.getByRole('heading', { name: /README\.md/i })).toBeVisible();

    // The real README.md should contain some content (it's a gitslice project)
    // Check for common markdown content that should be in the file
    const preview = page.locator('.file-preview');
    await expect(preview).toBeVisible();
    await expect(preview).not.toBeEmpty();
  });

  test('navigates into subdirectories and back', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();
    await page.getByRole('button', { name: /root_slice/i }).click();

    // Navigate to genesis root
    await page.getByRole('button', { name: /📁.*genesis/i }).click();

    // Navigate into internal/ subdirectory
    await page.getByRole('button', { name: /📁.*internal/i }).click();

    // Should see child entries under internal (type classification varies between runs)
    await expect(page.getByRole('button', { name: /common|config|models|services|storage/i }).first()).toBeVisible();
  });

  test('shows file sizes in the entry list', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();
    await page.getByRole('button', { name: /root_slice/i }).click();

    // Navigate to genesis root
    await page.getByRole('button', { name: /📁.*genesis/i }).click();

    // README.md should have a file size displayed (any valid size format)
    const readmeBtn = page.getByRole('button', { name: /README\.md/i });
    await expect(readmeBtn).toBeVisible();
    // Size should contain B, KB, or MB
    await expect(readmeBtn).toContainText(/\d+(\.\d+)?\s*(B|KB|MB)/);
  });
});

test.describe('Slice-specific Browsing (real server)', () => {
  test('browses root_slice in slice mode', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    // Select root_slice from the list (always exists after genesis)
    await page.getByRole('button', { name: /root_slice/i }).click();

    await expect(page.getByRole('heading', { name: /File tree/i })).toBeVisible();

    // Should see the "genesis" directory (genesis files)
    await expect(page.getByRole('button', { name: /📁.*genesis/i })).toBeVisible();
  });
});
