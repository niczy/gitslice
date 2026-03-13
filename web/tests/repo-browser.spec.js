import { test, expect } from '@playwright/test';

// Genesis populates files under o/genesis/projects/gitslice/
// so root entries should include the "o" directory.

async function ensureRootRepositoryVisible(page) {
  await page.getByTestId('slice-dropdown-trigger').click();
  await expect(page.getByTestId('slice-list')).toBeVisible();

  const rootSliceItem = page
    .getByTestId('slice-dropdown-item')
    .filter({ hasText: /root_slice|root slice|\broot\b/i })
    .first();

  if (await rootSliceItem.count()) {
    await rootSliceItem.click();
  } else {
    await page.keyboard.press('Escape');
  }
}

test.describe('Root Repository Browsing (real server)', () => {
  test('loads root entries and shows genesis directory tree', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    await ensureRootRepositoryVisible(page);

    // The navigator should render the full folder path, even at the root level.
    await expect(page.getByRole('button', { name: /📁.*\/o$/i })).toBeVisible();
  });

  test('navigates into genesis directory and finds repo files', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    await ensureRootRepositoryVisible(page);

    // Navigate: o -> genesis -> projects -> gitslice
    await page.getByRole('button', { name: /📁.*o/i }).click();
    await expect(page.getByRole('button', { name: /📁.*genesis/i })).toBeVisible();

    await page.getByRole('button', { name: /📁.*genesis/i }).click();
    await expect(page.getByRole('button', { name: /📁.*projects/i })).toBeVisible();

    await page.getByRole('button', { name: /📁.*projects/i }).click();
    await expect(page.getByRole('button', { name: /📁.*gitslice/i })).toBeVisible();

    await page.getByRole('button', { name: /📁.*gitslice/i }).click();

    // Should see real repo files
    await expect(page.getByRole('button', { name: /README\.md/i })).toBeVisible();
    await expect(page.getByRole('button', { name: /go\.mod/i })).toBeVisible();
    await expect(page.getByRole('button', { name: /Makefile/i })).toBeVisible();
  });

  test('previews a real file from the genesis repository', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    await ensureRootRepositoryVisible(page);

    // Navigate to gitslice root (wait for each level to load)
    await page.getByRole('button', { name: /📁.*o/i }).click();
    await expect(page.getByRole('button', { name: /📁.*genesis/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*genesis/i }).click();
    await expect(page.getByRole('button', { name: /📁.*projects/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*projects/i }).click();
    await expect(page.getByRole('button', { name: /📁.*gitslice/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*gitslice/i }).click();

    // Click README.md to preview it
    await page.getByRole('button', { name: /README\.md/i }).click();

    // The code header breadcrumbs should include the selected file.
    await expect(page.locator('.code-header .breadcrumb').filter({ hasText: /README\.md/i })).toBeVisible();

    // The real README.md should contain some content (it's a gitslice project)
    // Check for common markdown content that should be in the file
    const preview = page.locator('.file-preview');
    await expect(preview).toBeVisible();
    await expect(preview).not.toBeEmpty();
  });

  test('navigates into subdirectories and back', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    await ensureRootRepositoryVisible(page);

    // Navigate to gitslice root (wait for each level to load)
    await page.getByRole('button', { name: /📁.*o/i }).click();
    await expect(page.getByRole('button', { name: /📁.*genesis/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*genesis/i }).click();
    await expect(page.getByRole('button', { name: /📁.*projects/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*projects/i }).click();
    await expect(page.getByRole('button', { name: /📁.*gitslice/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*gitslice/i }).click();

    // Navigate into internal/ subdirectory
    await page.getByRole('button', { name: /📁.*internal/i }).click();

    // Should see child entries under internal (type classification varies between runs)
    await expect(page.getByRole('button', { name: /common|config|models|services|storage/i }).first()).toBeVisible();
  });


  test('keeps selected file open after refresh', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    await ensureRootRepositoryVisible(page);

    await page.getByRole('button', { name: /📁.*o/i }).click();
    await expect(page.getByRole('button', { name: /📁.*genesis/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*genesis/i }).click();
    await expect(page.getByRole('button', { name: /📁.*projects/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*projects/i }).click();
    await expect(page.getByRole('button', { name: /📁.*gitslice/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*gitslice/i }).click();

    await page.getByRole('button', { name: /README\.md/i }).click();
    await expect(page.locator('.code-header .breadcrumb').filter({ hasText: /README\.md/i })).toBeVisible();

    await page.reload();

    await page.getByTestId('topbar-repo-browser').click();
    await expect(page.locator('.code-header .breadcrumb').filter({ hasText: /README\.md/i })).toBeVisible();
  });
  test('shows file sizes in the entry list', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    await ensureRootRepositoryVisible(page);

    // Navigate to gitslice root (wait for each level to load)
    await page.getByRole('button', { name: /📁.*o/i }).click();
    await expect(page.getByRole('button', { name: /📁.*genesis/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*genesis/i }).click();
    await expect(page.getByRole('button', { name: /📁.*projects/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*projects/i }).click();
    await expect(page.getByRole('button', { name: /📁.*gitslice/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*gitslice/i }).click();

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

    await ensureRootRepositoryVisible(page);

    await expect(page.getByRole('heading', { name: /File tree/i })).toBeVisible();

    // Should see the "o" directory (genesis files)
    await expect(page.getByRole('button', { name: /📁.*o/i })).toBeVisible();
  });
});
