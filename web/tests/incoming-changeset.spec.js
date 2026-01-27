// @ts-check
import { test, expect } from '@playwright/test';

const sliceId = 'test-slice-1';

const changesetsResponse = {
  changesets: [
    {
      changeset_id: 'cs-001',
      changeset_hash: 'abc1234567890',
      slice_id: sliceId,
      base_commit_hash: 'base-commit-abc123',
      modified_files: ['src/main.go', 'src/utils.go'],
      status: 'PENDING',
      author: 'alice',
      created_at: 1704067200,
      message: 'Add utility functions',
    },
    {
      changeset_id: 'cs-002',
      changeset_hash: 'def4567890123',
      slice_id: sliceId,
      base_commit_hash: 'base-commit-def456',
      modified_files: ['README.md'],
      status: 'APPROVED',
      author: 'bob',
      created_at: 1704153600,
      message: 'Update documentation',
    },
  ],
};

const reviewReadyResponse = {
  changeset: changesetsResponse.changesets[0],
  diff: {
    files_added: 1,
    files_modified: 1,
    files_deleted: 0,
    lines_added: 42,
    lines_removed: 5,
  },
  review_status: 'READY_FOR_MERGE',
  warnings: [],
};

const reviewConflictResponse = {
  changeset: changesetsResponse.changesets[1],
  diff: {
    files_added: 0,
    files_modified: 1,
    files_deleted: 0,
    lines_added: 10,
    lines_removed: 2,
  },
  review_status: 'HAS_CONFLICTS',
  warnings: ['File README.md has been modified in another slice'],
};

const mergeSuccessResponse = {
  status: 'MERGE_STATUS_SUCCESS',
  new_commit_hash: 'new-commit-hash-12345',
  changeset_id: 'cs-001',
  conflicts: [],
};

const mergeConflictResponse = {
  status: 'MERGE_STATUS_CONFLICT',
  changeset_id: 'cs-002',
  conflicts: [
    { file_id: 'README.md', conflicting_slice_ids: ['other-slice'] },
  ],
};

/**
 * Navigate to the incoming changeset page by entering slice mode and clicking the button.
 */
async function navigateToChangesetPage(page) {
  // Mock file entries for the repo browser
  await page.route('**/v1/files/entries**', async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ entries: [] }),
    });
  });

  // Mock slice entries
  await page.route(`**/v1/slices/${sliceId}/entries**`, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ entries: [] }),
    });
  });

  await page.goto('/');
  await page.getByTestId('topbar-repo-browser').click();

  // Switch to slice mode
  await page.getByTestId('browse-mode').selectOption('slice');
  await page.getByTestId('slice-id').fill(sliceId);

  // Click view changesets button
  await page.getByTestId('view-changesets-btn').click();
}

test.describe('Incoming Changeset Page', () => {
  test('shows the view changesets button in slice mode', async ({ page }) => {
    await page.route('**/v1/files/entries**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ entries: [] }),
      });
    });

    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    // Button should not be visible in root mode
    await expect(page.getByTestId('view-changesets-btn')).not.toBeVisible();

    // Switch to slice mode and enter ID
    await page.getByTestId('browse-mode').selectOption('slice');
    await page.getByTestId('slice-id').fill(sliceId);
    await expect(page.getByTestId('view-changesets-btn')).toBeVisible();
  });

  test('navigates to changeset page and displays changeset list', async ({ page }) => {
    await page.route(`**/v1/slices/${sliceId}/changesets**`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(changesetsResponse),
      });
    });

    await navigateToChangesetPage(page);

    await expect(page.getByTestId('changeset-page')).toBeVisible();
    await expect(page.getByTestId('changeset-page-title')).toContainText(sliceId);

    const items = page.getByTestId('changeset-item');
    await expect(items).toHaveCount(2);
  });

  test('shows empty state when no changesets', async ({ page }) => {
    await page.route(`**/v1/slices/${sliceId}/changesets**`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ changesets: [] }),
      });
    });

    await navigateToChangesetPage(page);

    await expect(page.getByText('No incoming changesets for this slice.')).toBeVisible();
  });

  test('shows error when API fails', async ({ page }) => {
    await page.route(`**/v1/slices/${sliceId}/changesets**`, async (route) => {
      await route.fulfill({ status: 500 });
    });

    await navigateToChangesetPage(page);

    await expect(page.getByText('Unable to load changesets.')).toBeVisible();
  });

  test('selecting a changeset shows review details with ready status', async ({ page }) => {
    await page.route(`**/v1/slices/${sliceId}/changesets**`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(changesetsResponse),
      });
    });

    await page.route('**/v1/changesets/cs-001/review**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(reviewReadyResponse),
      });
    });

    await navigateToChangesetPage(page);

    // Click first changeset
    await page.getByTestId('changeset-select-btn').first().click();

    // Detail panel should show
    await expect(page.getByTestId('changeset-detail')).toBeVisible();
    await expect(page.getByTestId('changeset-review')).toBeVisible();
    await expect(page.getByTestId('review-status')).toContainText('Ready for merge');
    await expect(page.getByTestId('changeset-diff-summary')).toBeVisible();

    // Merge button should be enabled
    const mergeBtn = page.getByTestId('merge-btn');
    await expect(mergeBtn).toBeEnabled();
  });

  test('selecting a changeset with conflicts disables merge button', async ({ page }) => {
    await page.route(`**/v1/slices/${sliceId}/changesets**`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(changesetsResponse),
      });
    });

    await page.route('**/v1/changesets/cs-002/review**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(reviewConflictResponse),
      });
    });

    await navigateToChangesetPage(page);

    // Click second changeset
    await page.getByTestId('changeset-select-btn').nth(1).click();

    await expect(page.getByTestId('review-status')).toContainText('Has conflicts');
    await expect(page.getByTestId('merge-btn')).toBeDisabled();
    await expect(page.getByTestId('merge-hint')).toBeVisible();
  });

  test('successful merge shows success result', async ({ page }) => {
    await page.route(`**/v1/slices/${sliceId}/changesets**`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(changesetsResponse),
      });
    });

    await page.route('**/v1/changesets/cs-001/review**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(reviewReadyResponse),
      });
    });

    await page.route('**/v1/changesets/cs-001/merge', async (route) => {
      if (route.request().method() === 'POST') {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify(mergeSuccessResponse),
        });
      } else {
        await route.fallback();
      }
    });

    await navigateToChangesetPage(page);
    await page.getByTestId('changeset-select-btn').first().click();
    await expect(page.getByTestId('merge-btn')).toBeEnabled();

    await page.getByTestId('merge-btn').click();

    await expect(page.getByTestId('merge-result')).toBeVisible();
    await expect(page.getByTestId('merge-result')).toContainText('Merge successful');
    await expect(page.getByTestId('merge-result')).toContainText('new-commit-ha');
  });

  test('shows modified files list in detail panel', async ({ page }) => {
    await page.route(`**/v1/slices/${sliceId}/changesets**`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(changesetsResponse),
      });
    });

    await page.route('**/v1/changesets/cs-001/review**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(reviewReadyResponse),
      });
    });

    await navigateToChangesetPage(page);
    await page.getByTestId('changeset-select-btn').first().click();

    await expect(page.getByTestId('changeset-files-list')).toBeVisible();
    const fileItems = page.locator('.changeset-file-item');
    await expect(fileItems).toHaveCount(2);
    await expect(fileItems.first()).toContainText('src/main.go');
  });

  test('back button returns to repo browser', async ({ page }) => {
    await page.route(`**/v1/slices/${sliceId}/changesets**`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(changesetsResponse),
      });
    });

    await navigateToChangesetPage(page);
    await expect(page.getByTestId('changeset-page')).toBeVisible();

    await page.getByTestId('changeset-back-btn').click();
    await expect(page.getByTestId('changeset-page')).not.toBeVisible();
  });

  test('shows warnings from review', async ({ page }) => {
    await page.route(`**/v1/slices/${sliceId}/changesets**`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(changesetsResponse),
      });
    });

    await page.route('**/v1/changesets/cs-002/review**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(reviewConflictResponse),
      });
    });

    await navigateToChangesetPage(page);
    await page.getByTestId('changeset-select-btn').nth(1).click();

    await expect(page.getByTestId('changeset-warnings')).toBeVisible();
    await expect(page.getByText('File README.md has been modified in another slice')).toBeVisible();
  });
});
