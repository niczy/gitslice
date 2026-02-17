// @ts-check
import { test, expect } from '@playwright/test';

// Helper: navigate to a genesis file and open its history panel.
async function openGenesisHistory(page) {
  await page.goto('/');
  await page.getByTestId('topbar-repo-browser').click();
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

  // Select README.md and open history
  await expect(page.getByRole('button', { name: /README\.md/i })).toBeVisible();
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

  test('renders unified patch content for changed files', async ({ page }) => {
    const commitHash = 'commit-test-patch';
    await page.route(`**/v1/commits/${commitHash}/changes*`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          commit_hash: commitHash,
          files_added: 0,
          files_modified: 1,
          files_deleted: 0,
          files_renamed: 0,
          changes: [
            {
              id: 'change-1',
              path: 'README.md',
              change_type: 'CHANGE_TYPE_MODIFY',
              lines_added: 1,
              lines_deleted: 1,
              patch: '--- a/README.md\n+++ b/README.md\n@@ -1 +1 @@\n-Hello\n+Hello world\n',
            },
          ],
        }),
      });
    });

    await page.goto(`/#/diff/${commitHash}`);
    await expect(page.getByTestId('commit-diff-page')).toBeVisible();

    const patch = page.getByTestId('diff-file-patch').first();
    await expect(patch).toBeVisible();
    await expect(patch).toContainText(/--- a\//);
    await expect(patch).toContainText(/\+\+\+ b\//);

    const addedLine = patch.locator('.diff-line-added').first();
    await expect(addedLine).toBeVisible();
  });

  test('scrolls diff content container when selecting a file from the panel', async ({ page }) => {
    const commitHash = 'commit-test-scroll';
    const changes = Array.from({ length: 35 }, (_, index) => ({
      id: `change-${index}`,
      path: `src/deep/path/file-${index}.txt`,
      change_type: 'CHANGE_TYPE_MODIFY',
      lines_added: 1,
      lines_deleted: 0,
      patch: `@@ -1 +1 @@\n-old ${index}\n+new ${index}\n`,
    }));

    await page.route(`**/v1/commits/${commitHash}/changes*`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          commit_hash: commitHash,
          files_added: 0,
          files_modified: changes.length,
          files_deleted: 0,
          files_renamed: 0,
          changes,
        }),
      });
    });

    await page.goto(`/#/diff/${commitHash}`);
    await expect(page.getByTestId('commit-diff-page')).toBeVisible();

    const diffContent = page.locator('.diff-content');
    await expect(diffContent).toBeVisible();
    await expect.poll(async () => diffContent.evaluate((el) => el.scrollTop)).toBe(0);

    const targetIndex = 30;
    await page.getByTestId('diff-file-panel-item').nth(targetIndex).click();

    await expect.poll(async () => diffContent.evaluate((el) => el.scrollTop)).toBeGreaterThan(0);
    await expect
      .poll(async () => page.evaluate(() => window.scrollY))
      .toBeLessThan(80);
  });

  test('loads file content fallback when patch data is unavailable', async ({ page }) => {
    const commitHash = 'commit-test-fallback-content';
    await page.route(`**/v1/commits/${commitHash}/changes*`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          commit_hash: commitHash,
          files_added: 1,
          files_modified: 0,
          files_deleted: 0,
          files_renamed: 0,
          changes: [
            {
              id: 'change-fallback',
              slice_id: 'root_slice',
              path: 'README.md',
              change_type: 'CHANGE_TYPE_ADD',
              lines_added: 2,
              lines_deleted: 0,
              patch: '',
            },
          ],
        }),
      });
    });

    await page.route('**/v1/slices/root_slice/files/README.md?slice_version.slice_hash=*', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          file: {
            path: 'README.md',
            content: 'SGVsbG8gd29ybGQKc2Vjb25kIGxpbmU=',
            size: 23,
            hash: 'hash-1',
          },
        }),
      });
    });

    await page.goto(`/#/diff/${commitHash}`);
    await expect(page.getByTestId('commit-diff-page')).toBeVisible();

    const fallback = page.getByTestId('diff-file-fallback-content').first();
    await expect(fallback).toBeVisible();
    await expect(fallback).toContainText('Hello world');
    await expect(fallback.locator('.diff-line-added').first()).toBeVisible();
  });

  test('hides binary patch content behind an explicit action', async ({ page }) => {
    const commitHash = 'commit-test-binary-patch';
    await page.route(`**/v1/commits/${commitHash}/changes*`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          commit_hash: commitHash,
          files_added: 0,
          files_modified: 1,
          files_deleted: 0,
          files_renamed: 0,
          changes: [
            {
              id: 'binary-change-1',
              slice_id: 'root_slice',
              path: 'assets/logo.png',
              change_type: 'CHANGE_TYPE_MODIFY',
              lines_added: 0,
              lines_deleted: 0,
              patch: 'diff --git a/assets/logo.png b/assets/logo.png\nBinary files a/assets/logo.png and b/assets/logo.png differ',
            },
          ],
        }),
      });
    });

    await page.route('**/v1/slices/root_slice/files/assets/logo.png?slice_version.slice_hash=*', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          file: {
            path: 'assets/logo.png',
            content: 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADElEQVR4nGNgZGJmAAACbgD5Y0iH5QAAAABJRU5ErkJggg==',
          },
        }),
      });
    });

    await page.goto(`/#/diff/${commitHash}`);
    await expect(page.getByTestId('commit-diff-page')).toBeVisible();

    const binaryBlock = page.getByTestId('diff-file-binary-block').first();
    await expect(binaryBlock).toContainText('binary content');
    await expect(page.getByTestId('diff-file-patch')).toHaveCount(0);

    await page.getByTestId('diff-file-view-binary-btn').first().click();
    await expect(page.getByTestId('diff-file-patch').first()).toContainText('Binary files');
  });

  test('renders binary fallback as an image after user opt-in', async ({ page }) => {
    const commitHash = 'commit-test-binary-fallback';
    await page.route(`**/v1/commits/${commitHash}/changes*`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          commit_hash: commitHash,
          files_added: 1,
          files_modified: 0,
          files_deleted: 0,
          files_renamed: 0,
          changes: [
            {
              id: 'binary-add-1',
              slice_id: 'root_slice',
              path: 'assets/new-logo.png',
              change_type: 'CHANGE_TYPE_ADD',
              lines_added: 0,
              lines_deleted: 0,
              patch: '',
            },
          ],
        }),
      });
    });

    await page.route('**/v1/slices/root_slice/files/assets/new-logo.png?slice_version.slice_hash=*', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          file: {
            path: 'assets/new-logo.png',
            content: 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADElEQVR4nGNgZGJmAAACbgD5Y0iH5QAAAABJRU5ErkJggg==',
          },
        }),
      });
    });

    await page.goto(`/#/diff/${commitHash}`);
    await expect(page.getByTestId('commit-diff-page')).toBeVisible();

    await expect(page.getByTestId('diff-file-binary-block').first()).toContainText('hidden by default');
    await expect(page.getByTestId('diff-file-binary-preview')).toHaveCount(0);

    await page.getByTestId('diff-file-view-binary-btn').first().click();
    await expect(page.getByTestId('diff-file-binary-preview').locator('img')).toBeVisible();
  });

});
