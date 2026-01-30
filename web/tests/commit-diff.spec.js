// @ts-check
import { test, expect } from '@playwright/test';

// Helper: navigate to a genesis file and open its history panel.
async function openGenesisHistory(page) {
  await page.goto('/');
  await page.getByTestId('topbar-repo-browser').click();

  // Navigate: o -> genesis -> projects -> gitslice
  await page.getByRole('button', { name: /📁.*o/i }).click();
  await page.getByRole('button', { name: /📁.*genesis/i }).click();
  await page.getByRole('button', { name: /📁.*projects/i }).click();
  await page.getByRole('button', { name: /📁.*gitslice/i }).click();

  // Select README.md and open history
  await page.getByRole('button', { name: /README\.md/i }).click();
  const preview = page.locator('.file-preview');
  await expect(preview).toBeVisible();

  await page.getByTestId('history-toggle').click();
  await expect(page.getByTestId('history-panel')).toBeVisible();
}

test.describe('Commit Diff Page (real server)', () => {
  test('navigates to diff page when clicking commit hash link', async ({ page }) => {
    await openGenesisHistory(page);

    // Click the commit hash link in the first history item
    const diffLink = page.getByTestId('commit-diff-link').first();
    await expect(diffLink).toBeVisible();
    await diffLink.click();

    // Should navigate to the diff page
    await expect(page.getByTestId('commit-diff-page')).toBeVisible();
    await expect(page.getByTestId('diff-commit-title')).toBeVisible();
  });

  test('displays genesis commit diff with file changes', async ({ page }) => {
    await openGenesisHistory(page);
    await page.getByTestId('commit-diff-link').first().click();

    await expect(page.getByTestId('commit-diff-page')).toBeVisible();

    // Check summary stats — genesis is all adds
    const summary = page.getByTestId('diff-summary');
    await expect(summary).toBeVisible();
    // Genesis adds files, so "added" count should be > 0
    await expect(summary).toContainText(/\+\d+ added/);

    // Check file list has entries
    const fileItems = page.getByTestId('diff-file-item');
    const count = await fileItems.count();
    expect(count).toBeGreaterThan(0);

    // All genesis entries should be "Add" type
    const firstItem = fileItems.first();
    await expect(firstItem.locator('.change-type')).toHaveText('Add');

    // Each item should show a file path
    const firstPath = page.getByTestId('diff-file-path').first();
    await expect(firstPath).not.toBeEmpty();
  });

  test('back button returns to repo browser', async ({ page }) => {
    await openGenesisHistory(page);
    await page.getByTestId('commit-diff-link').first().click();

    await expect(page.getByTestId('commit-diff-page')).toBeVisible();

    // Click back
    await page.getByTestId('diff-back-btn').click();

    // Should be back on browser
    await expect(page.getByTestId('commit-diff-page')).not.toBeVisible();
  });

  test('displays line stats for changed files', async ({ page }) => {
    await openGenesisHistory(page);
    await page.getByTestId('commit-diff-link').first().click();

    await expect(page.getByTestId('commit-diff-page')).toBeVisible();

    // Check the first file item has line stats
    const firstItem = page.getByTestId('diff-file-item').first();
    await expect(firstItem.locator('.lines-added')).toBeVisible();
    // Genesis adds files, so lines_added should be "+N" with N > 0
    await expect(firstItem.locator('.lines-added')).toContainText(/\+\d+/);
  });
});
