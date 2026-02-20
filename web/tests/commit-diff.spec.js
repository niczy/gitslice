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


  test('defers patch fetch for long diffs until user scrolls', async ({ page }) => {
    const commitHash = 'commit-test-lazy-scroll';
    const changes = Array.from({ length: 120 }, (_, index) => ({
      id: `lazy-${index}`,
      slice_id: 'root_slice',
      path: `src/file-${index}.txt`,
      change_type: 'CHANGE_TYPE_MODIFY',
      lines_added: 2,
      lines_deleted: 1,
      patch: '',
    }));

    let withPatchesRequested = false;
    await page.route(`**/v1/commits/${commitHash}/changes*`, async (route) => {
      if (route.request().url().includes('include_patches=true')) {
        withPatchesRequested = true;
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            commit_hash: commitHash,
            files_added: 0,
            files_modified: changes.length,
            files_deleted: 0,
            files_renamed: 0,
            changes: changes.map((change) => ({
              ...change,
              patch: `--- a/${change.path}\n+++ b/${change.path}\n@@ -1 +1 @@\n-old\n+new\n`,
            })),
          }),
        });
        return;
      }

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
    await expect(page.getByTestId('diff-patch-lazy-state')).toBeVisible();
    expect(withPatchesRequested).toBe(false);

    const diffContent = page.locator('.diff-content');
    await diffContent.evaluate((el) => { el.scrollTop = 80; el.dispatchEvent(new Event('scroll')); });

    await expect.poll(() => withPatchesRequested).toBe(true);
    await expect(page.getByTestId('diff-file-patch').first()).toBeVisible();
  });

  test('revert flow creates a changeset, merges it, and refreshes browser content', async ({ page }) => {
    const commitHash = 'commit-revert-e2e';
    const changesetId = 'cs-revert-e2e';
    const beforeText = 'line one\\n';
    const changedText = 'line one\\nline two\\n';
    const toB64 = (value) => Buffer.from(value, 'utf8').toString('base64');
    let currentReadmeContent = changedText;

    await page.route('**/v1/slices?limit=200', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          slices: [
            {
              slice_id: 'root_slice',
              name: 'root_slice',
              description: 'root',
              files: ['README.md'],
              owners: ['system'],
              created_by: 'system',
              is_root: true,
            },
          ],
        }),
      });
    });

    await page.route('**/v1/slices/root_slice/entries*', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            {
              id: 'root_slice:README.md',
              name: 'README.md',
              path: 'README.md',
              type: 'file',
              size: currentReadmeContent.length,
            },
          ],
        }),
      });
    });

    await page.route('**/v1/slices/root_slice/files/history/README.md', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          changes: [
            {
              id: 'history-change-1',
              slice_id: 'root_slice',
              commit_hash: commitHash,
              path: 'README.md',
              change_type: 'CHANGE_TYPE_MODIFY',
              old_hash: 'hash-before',
              new_hash: 'hash-after',
              lines_added: 1,
              lines_deleted: 0,
              author: 'tester',
              message: 'add second line',
              timestamp: `${Math.floor(Date.now() / 1000)}`,
            },
          ],
        }),
      });
    });

    await page.route('**/v1/slices/root_slice/files/README.md*', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          file: {
            path: 'README.md',
            content: toB64(currentReadmeContent),
            size: currentReadmeContent.length,
            hash: currentReadmeContent === beforeText ? 'hash-before' : 'hash-after',
          },
        }),
      });
    });

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
              id: 'change-revert-target',
              slice_id: 'root_slice',
              commit_hash: commitHash,
              path: 'README.md',
              change_type: 'CHANGE_TYPE_MODIFY',
              old_hash: 'hash-before',
              new_hash: 'hash-after',
              lines_added: 1,
              lines_deleted: 0,
              author: 'tester',
              message: 'add second line',
              patch: '--- a/README.md\\n+++ b/README.md\\n@@ -1 +1,2 @@\\n line one\\n+line two\\n',
            },
          ],
        }),
      });
    });

    await page.route(`**/v1/commits/${commitHash}/changes/revert`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          changeset_id: changesetId,
          changeset_hash: 'revert~commit-revert-e2e~*~123',
          status: 'PENDING',
        }),
      });
    });

    await page.route(`**/v1/changesets/${changesetId}/diff`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          changeset: {
            changeset_id: changesetId,
            slice_id: 'root_slice',
            status: 'PENDING',
            author: 'tester',
            created_at: `${Math.floor(Date.now() / 1000)}`,
            message: `Revert commit ${commitHash}`,
          },
          diff: {
            files_added: 0,
            files_modified: 1,
            files_deleted: 0,
            lines_added: 0,
            lines_removed: 1,
          },
          changes: [
            {
              id: 'revert-entry-1',
              slice_id: 'root_slice',
              path: 'README.md',
              change_type: 'CHANGE_TYPE_MODIFY',
              old_hash: 'hash-after',
              new_hash: 'hash-before',
              lines_added: 0,
              lines_deleted: 1,
              patch: '--- a/README.md\\n+++ b/README.md\\n@@ -1,2 +1 @@\\n line one\\n-line two\\n',
            },
          ],
        }),
      });
    });

    await page.route(`**/v1/changesets/${changesetId}/merge`, async (route) => {
      currentReadmeContent = beforeText;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          status: 'MERGE_STATUS_SUCCESS',
          new_commit_hash: 'commit-after-revert',
          changeset_id: changesetId,
          conflicts: [],
        }),
      });
    });

    await page.goto('/#/browser');
    await expect(page.getByRole('button', { name: /README\.md/i })).toBeVisible();
    await page.getByRole('button', { name: /README\.md/i }).click();
    await expect(page.locator('.file-preview')).toContainText('line two');

    await page.getByTestId('history-toggle').click();
    await expect(page.getByTestId('history-panel')).toBeVisible();
    await page.getByTestId('commit-diff-link').first().click();

    await expect(page.getByTestId('commit-diff-page')).toBeVisible();
    await page.getByTestId('diff-revert-btn').click();

    await expect(page.getByTestId('changeset-diff-page')).toBeVisible();
    await expect(page.getByTestId('changeset-file-item').first().locator('.diff-patch')).toContainText('-line two');
    await page.getByTestId('changeset-merge-btn').click();

    await expect(page.getByTestId('commit-diff-page')).toHaveCount(0);
    await expect(page.getByTestId('changeset-diff-page')).toHaveCount(0);
    await expect(page.locator('.file-preview')).toContainText(/^line one$/);
    await expect(page.locator('.file-preview')).not.toContainText('line two');
  });

});
