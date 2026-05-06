import { test, expect } from '@playwright/test';

// Helper: navigate from browser root to the gitslice project directory
// and select a specific file.
async function navigateToGenesisFile(page, fileName) {
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

  // Navigate: o -> genesis -> projects -> gitslice (wait for each level to load)
  await page.getByRole('button', { name: /📁.*o/i }).click();
  await expect(page.getByRole('button', { name: /📁.*genesis/i })).toBeVisible();
  await page.getByRole('button', { name: /📁.*genesis/i }).click();
  await expect(page.getByRole('button', { name: /📁.*projects/i })).toBeVisible();
  await page.getByRole('button', { name: /📁.*projects/i }).click();
  await expect(page.getByRole('button', { name: /📁.*gitslice/i })).toBeVisible();
  await page.getByRole('button', { name: /📁.*gitslice/i }).click();

  // Select the requested file
  const fileRegex = new RegExp(fileName.replace('.', '\\.'), 'i');
  await expect(page.getByRole('button', { name: fileRegex })).toBeVisible();
  await page.getByRole('button', { name: fileRegex }).click();
}

test.describe('File History (real server)', () => {
  test('shows history toggle button when file is selected', async ({ page }) => {
    await navigateToGenesisFile(page, 'README.md');

    // File content should be visible
    const preview = page.locator('.file-preview');
    await expect(preview).toBeVisible();

    // History toggle should now be visible
    await expect(page.getByTestId('history-toggle')).toBeVisible();
    await expect(page.getByTestId('history-toggle')).toHaveText(/History/);
  });

  test('toggles between content and history view', async ({ page }) => {
    await navigateToGenesisFile(page, 'README.md');

    // Click history toggle
    await page.getByTestId('history-toggle').click();

    // Should show history panel
    await expect(page.getByTestId('history-panel')).toBeVisible();
    await expect(page.getByTestId('history-toggle')).toHaveText(/Content/);

    // Should display at least one history item (the genesis commit)
    const historyItems = page.getByTestId('history-item');
    await expect(historyItems.first()).toBeVisible();

    // Genesis commit should show expected metadata
    const firstItem = historyItems.first();
    await expect(firstItem.getByText('Genesis: initialize repository files')).toBeVisible();
    await expect(firstItem.getByText('system')).toBeVisible();
    await expect(firstItem.locator('.change-type')).toHaveText('Add');

    // Toggle back to content
    await page.getByTestId('history-toggle').click();
    await expect(page.getByTestId('history-panel')).not.toBeVisible();
    await expect(page.getByTestId('history-toggle')).toHaveText(/History/);
  });

  test('shows genesis commit details with line counts', async ({ page }) => {
    await navigateToGenesisFile(page, 'go.mod');

    await page.getByTestId('history-toggle').click();
    await expect(page.getByTestId('history-panel')).toBeVisible();

    const historyItems = page.getByTestId('history-item');
    await expect(historyItems.first()).toBeVisible();

    // Genesis adds files, so lines_added should be positive
    const firstItem = historyItems.first();
    await expect(firstItem.locator('.lines-added')).toBeVisible();
    // Lines added format is "+N" where N > 0
    await expect(firstItem.locator('.lines-added')).toContainText(/\+\d+/);
  });

  test('resets history when selecting a different file', async ({ page }) => {
    await navigateToGenesisFile(page, 'README.md');

    // Open history for first file
    await page.getByTestId('history-toggle').click();
    await expect(page.getByTestId('history-panel')).toBeVisible();

    // Select a different file (go.mod)
    await page.getByRole('button', { name: /go\.mod/i }).click();

    // Should reset to content view
    await expect(page.getByTestId('history-panel')).not.toBeVisible();
    await expect(page.getByTestId('history-toggle')).toHaveText(/History/);
  });

  test('shows genesis history with commit hash link', async ({ page }) => {
    await navigateToGenesisFile(page, 'README.md');

    await page.getByTestId('history-toggle').click();
    await expect(page.getByTestId('history-panel')).toBeVisible();

    // Genesis commit should have a clickable commit hash
    const historyItems = page.getByTestId('history-item');
    const firstItem = historyItems.first();
    const commitLink = firstItem.getByTestId('commit-diff-link');
    await expect(commitLink).toBeVisible();
    // The commit hash should be displayed (truncated)
    await expect(firstItem.locator('.commit-hash')).toBeVisible();
  });
});

test.describe('File History (Slice Mode)', () => {
  test('shows history for root_slice files', async ({ page }) => {
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

    // Navigate to a file: o -> genesis -> projects -> gitslice -> README.md
    await page.getByRole('button', { name: /📁.*o/i }).click();
    await expect(page.getByRole('button', { name: /📁.*genesis/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*genesis/i }).click();
    await expect(page.getByRole('button', { name: /📁.*projects/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*projects/i }).click();
    await expect(page.getByRole('button', { name: /📁.*gitslice/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*gitslice/i }).click();
    await expect(page.getByRole('button', { name: /README\.md/i })).toBeVisible();
    await page.getByRole('button', { name: /README\.md/i }).click();

    // Open history
    await page.getByTestId('history-toggle').click();
    await expect(page.getByTestId('history-panel')).toBeVisible();

    // Should show genesis history entry
    const historyItems = page.getByTestId('history-item');
    await expect(historyItems.first()).toBeVisible();
    await expect(historyItems.first().getByText('Genesis: initialize repository files')).toBeVisible();
  });
});
