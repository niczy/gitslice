// @ts-check
import { test, expect } from '@playwright/test';

const genesisCommitHash = 'genesis-commit-1234567890';

const genesisChangesResponse = {
  commit_hash: genesisCommitHash,
  changes: [
    {
      id: 'genesis-o/genesis/projects/gitslice/README.md',
      slice_id: 'root_slice',
      commit_hash: genesisCommitHash,
      path: 'o/genesis/projects/gitslice/README.md',
      change_type: 'CHANGE_TYPE_ADD',
      lines_added: 10,
      lines_deleted: 0,
      author: 'system',
      message: 'Genesis: initialize repository files',
      timestamp: 1704067200,
    },
    {
      id: 'genesis-o/genesis/projects/gitslice/main.go',
      slice_id: 'root_slice',
      commit_hash: genesisCommitHash,
      path: 'o/genesis/projects/gitslice/main.go',
      change_type: 'CHANGE_TYPE_ADD',
      lines_added: 25,
      lines_deleted: 0,
      author: 'system',
      message: 'Genesis: initialize repository files',
      timestamp: 1704067200,
    },
    {
      id: 'genesis-o/genesis/projects/gitslice/go.mod',
      slice_id: 'root_slice',
      commit_hash: genesisCommitHash,
      path: 'o/genesis/projects/gitslice/go.mod',
      change_type: 'CHANGE_TYPE_ADD',
      lines_added: 5,
      lines_deleted: 0,
      author: 'system',
      message: 'Genesis: initialize repository files',
      timestamp: 1704067200,
    },
  ],
  files_added: 3,
  files_modified: 0,
  files_deleted: 0,
  files_renamed: 0,
};

/**
 * Helper: set up mocks for browsing genesis files + history, then navigate
 * to the history panel for a genesis file.
 */
async function setupGenesisAndOpenHistory(page) {
  // Mock file entries
  await page.route('**/v1/files/entries**', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        entries: [
          { name: 'README.md', path: 'o/genesis/projects/gitslice/README.md', type: 'ENTRY_TYPE_FILE', size: 100 },
        ],
      }),
    });
  });

  // Mock file content
  await page.route('**/v1/files/**README.md', async (route) => {
    if (route.request().url().includes('/history/')) return route.fallback();
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        file: {
          path: 'o/genesis/projects/gitslice/README.md',
          content: Buffer.from('# Gitslice').toString('base64'),
          size: 10,
        },
      }),
    });
  });

  // Mock file history with genesis commit
  await page.route('**/v1/files/history/**README.md', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        changes: [
          {
            id: 'genesis-o/genesis/projects/gitslice/README.md',
            slice_id: 'root_slice',
            commit_hash: genesisCommitHash,
            path: 'o/genesis/projects/gitslice/README.md',
            change_type: 'CHANGE_TYPE_ADD',
            lines_added: 10,
            lines_deleted: 0,
            author: 'system',
            message: 'Genesis: initialize repository files',
            timestamp: 1704067200,
          },
        ],
        has_more: false,
      }),
    });
  });

  // Mock commit changes endpoint
  await page.route('**/v1/commits/*/changes', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(genesisChangesResponse),
    });
  });

  await page.goto('/');
  await page.getByTestId('topbar-repo-browser').click();

  // Select genesis file and open history
  await page.getByRole('button', { name: /README\.md/i }).click();
  await expect(page.getByText('# Gitslice')).toBeVisible();
  await page.getByTestId('history-toggle').click();
  await expect(page.getByTestId('history-panel')).toBeVisible();
}

test.describe('Commit Diff Page', () => {
  test('navigates to diff page when clicking commit hash link in genesis history', async ({ page }) => {
    await setupGenesisAndOpenHistory(page);

    // Click the commit hash link
    const diffLink = page.getByTestId('commit-diff-link').first();
    await expect(diffLink).toBeVisible();
    await diffLink.click();

    // Should navigate to the diff page
    await expect(page.getByTestId('commit-diff-page')).toBeVisible();
    await expect(page.getByTestId('diff-commit-title')).toContainText('genesis-comm');
  });

  test('displays commit diff with correct file changes', async ({ page }) => {
    await setupGenesisAndOpenHistory(page);
    await page.getByTestId('commit-diff-link').first().click();

    await expect(page.getByTestId('commit-diff-page')).toBeVisible();

    // Check summary stats
    const summary = page.getByTestId('diff-summary');
    await expect(summary).toBeVisible();
    await expect(summary).toContainText('+3 added');
    await expect(summary).toContainText('0 modified');
    await expect(summary).toContainText('-0 deleted');

    // Check file list
    const fileItems = page.getByTestId('diff-file-item');
    await expect(fileItems).toHaveCount(3);

    // Verify file paths are displayed
    const filePaths = page.getByTestId('diff-file-path');
    await expect(filePaths.nth(0)).toHaveText('o/genesis/projects/gitslice/README.md');
    await expect(filePaths.nth(1)).toHaveText('o/genesis/projects/gitslice/main.go');
    await expect(filePaths.nth(2)).toHaveText('o/genesis/projects/gitslice/go.mod');

    // All should be "Add" type
    const changeTypes = page.locator('.diff-file-item .change-type');
    await expect(changeTypes.nth(0)).toHaveText('Add');
    await expect(changeTypes.nth(1)).toHaveText('Add');
    await expect(changeTypes.nth(2)).toHaveText('Add');
  });

  test('back button returns to repo browser', async ({ page }) => {
    await setupGenesisAndOpenHistory(page);
    await page.getByTestId('commit-diff-link').first().click();

    await expect(page.getByTestId('commit-diff-page')).toBeVisible();

    // Click back
    await page.getByTestId('diff-back-btn').click();

    // Should be back on browser (history panel visible from before)
    await expect(page.getByTestId('commit-diff-page')).not.toBeVisible();
  });

  test('shows error state when commit changes API fails', async ({ page }) => {
    // Mock entries and file
    await page.route('**/v1/files/entries**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            { name: 'README.md', path: 'o/genesis/projects/gitslice/README.md', type: 'ENTRY_TYPE_FILE', size: 100 },
          ],
        }),
      });
    });

    await page.route('**/v1/files/**README.md', async (route) => {
      if (route.request().url().includes('/history/')) return route.fallback();
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          file: { path: 'o/genesis/projects/gitslice/README.md', content: Buffer.from('# Gitslice').toString('base64'), size: 10 },
        }),
      });
    });

    await page.route('**/v1/files/history/**README.md', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          changes: [{
            id: 'genesis-readme',
            commit_hash: genesisCommitHash,
            path: 'o/genesis/projects/gitslice/README.md',
            change_type: 'CHANGE_TYPE_ADD',
            lines_added: 10,
            lines_deleted: 0,
            author: 'system',
            message: 'Genesis: initialize repository files',
            timestamp: 1704067200,
          }],
          has_more: false,
        }),
      });
    });

    // Return error for commit changes
    await page.route('**/v1/commits/*/changes', async (route) => {
      await route.fulfill({ status: 500, contentType: 'application/json', body: '{}' });
    });

    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();
    await page.getByRole('button', { name: /README\.md/i }).click();
    await page.getByTestId('history-toggle').click();
    await page.getByTestId('commit-diff-link').first().click();

    await expect(page.getByTestId('commit-diff-page')).toBeVisible();
    await expect(page.getByText('Unable to load commit changes.')).toBeVisible();
  });

  test('displays line stats for each changed file', async ({ page }) => {
    await setupGenesisAndOpenHistory(page);
    await page.getByTestId('commit-diff-link').first().click();

    await expect(page.getByTestId('commit-diff-page')).toBeVisible();

    // Check the first file item has line stats
    const firstItem = page.getByTestId('diff-file-item').first();
    await expect(firstItem.locator('.lines-added')).toHaveText('+10');
    await expect(firstItem.locator('.lines-deleted')).toHaveText('-0');

    // Check second file
    const secondItem = page.getByTestId('diff-file-item').nth(1);
    await expect(secondItem.locator('.lines-added')).toHaveText('+25');
    await expect(secondItem.locator('.lines-deleted')).toHaveText('-0');
  });
});
