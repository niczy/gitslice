import { test, expect } from '@playwright/test';

test.describe('File History (Root Mode)', () => {
  test('shows history toggle button when file is selected', async ({ page }) => {
    // Mock entries
    await page.route('**/v1/files/entries**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            { name: 'readme.md', path: 'readme.md', type: 'ENTRY_TYPE_FILE', size: 50 },
          ],
        }),
      });
    });

    // Mock file content
    await page.route('**/v1/files/readme.md', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          file: {
            path: 'readme.md',
            content: Buffer.from('# Hello World').toString('base64'),
            size: 13,
          },
        }),
      });
    });

    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    // History toggle should not be visible before selecting a file
    await expect(page.getByTestId('history-toggle')).not.toBeVisible();

    // Select a file
    await page.getByRole('button', { name: /readme\.md/i }).click();
    await expect(page.getByText('# Hello World')).toBeVisible();

    // History toggle should now be visible
    await expect(page.getByTestId('history-toggle')).toBeVisible();
    await expect(page.getByTestId('history-toggle')).toHaveText(/History/);
  });

  test('toggles between content and history view', async ({ page }) => {
    // Mock entries
    await page.route('**/v1/files/entries**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            { name: 'app.js', path: 'app.js', type: 'ENTRY_TYPE_FILE', size: 100 },
          ],
        }),
      });
    });

    // Mock file content
    await page.route('**/v1/files/app.js', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          file: {
            path: 'app.js',
            content: Buffer.from('const x = 1;').toString('base64'),
            size: 12,
          },
        }),
      });
    });

    // Mock file history
    await page.route('**/v1/files/history/app.js', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          changes: [
            {
              id: 'change-1',
              slice_id: 'root_slice',
              commit_hash: 'abc1234567890',
              path: 'app.js',
              change_type: 'CHANGE_TYPE_MODIFY',
              lines_added: 5,
              lines_deleted: 2,
              author: 'developer@example.com',
              message: 'Fix bug in app initialization',
              timestamp: 1704067200,
            },
            {
              id: 'change-2',
              slice_id: 'root_slice',
              commit_hash: 'def9876543210',
              path: 'app.js',
              change_type: 'CHANGE_TYPE_ADD',
              lines_added: 10,
              lines_deleted: 0,
              author: 'developer@example.com',
              message: 'Initial commit',
              timestamp: 1703980800,
            },
          ],
          has_more: false,
        }),
      });
    });

    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    // Select file
    await page.getByRole('button', { name: /app\.js/i }).click();
    await expect(page.getByText('const x = 1;')).toBeVisible();

    // Click history toggle
    await page.getByTestId('history-toggle').click();

    // Should show history panel
    await expect(page.getByTestId('history-panel')).toBeVisible();
    await expect(page.getByTestId('history-toggle')).toHaveText(/Content/);

    // Should display history items
    const historyItems = page.getByTestId('history-item');
    await expect(historyItems).toHaveCount(2);

    // Verify first history item content
    const firstHistoryItem = historyItems.first();
    await expect(firstHistoryItem.getByText('Fix bug in app initialization')).toBeVisible();
    await expect(firstHistoryItem.getByText('abc1234')).toBeVisible();
    await expect(firstHistoryItem.getByText('developer@example.com')).toBeVisible();
    await expect(firstHistoryItem.locator('.change-type')).toHaveText('Modify');

    // Verify line changes are shown
    await expect(page.getByText('+5')).toBeVisible();
    await expect(page.getByText('-2')).toBeVisible();

    // Toggle back to content
    await page.getByTestId('history-toggle').click();
    await expect(page.getByTestId('history-panel')).not.toBeVisible();
    await expect(page.getByText('const x = 1;')).toBeVisible();
    await expect(page.getByTestId('history-toggle')).toHaveText(/History/);
  });

  test('shows empty state when no history available', async ({ page }) => {
    // Mock entries
    await page.route('**/v1/files/entries**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            { name: 'new.txt', path: 'new.txt', type: 'ENTRY_TYPE_FILE', size: 10 },
          ],
        }),
      });
    });

    // Mock file content
    await page.route('**/v1/files/new.txt', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          file: {
            path: 'new.txt',
            content: Buffer.from('content').toString('base64'),
            size: 7,
          },
        }),
      });
    });

    // Mock empty history
    await page.route('**/v1/files/history/new.txt', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          changes: [],
          has_more: false,
        }),
      });
    });

    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();
    await page.getByRole('button', { name: /new\.txt/i }).click();
    await page.getByTestId('history-toggle').click();

    await expect(page.getByTestId('history-panel')).toBeVisible();
    await expect(page.getByText('No history available for this file.')).toBeVisible();
  });

  test('shows error when history fetch fails', async ({ page }) => {
    // Mock entries
    await page.route('**/v1/files/entries**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            { name: 'error.txt', path: 'error.txt', type: 'ENTRY_TYPE_FILE', size: 10 },
          ],
        }),
      });
    });

    // Mock file content
    await page.route('**/v1/files/error.txt', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          file: {
            path: 'error.txt',
            content: Buffer.from('content').toString('base64'),
            size: 7,
          },
        }),
      });
    });

    // Mock history error
    await page.route('**/v1/files/history/error.txt', async (route) => {
      await route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'Internal server error' }),
      });
    });

    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();
    await page.getByRole('button', { name: /error\.txt/i }).click();
    await page.getByTestId('history-toggle').click();

    await expect(page.getByText('Unable to load file history.')).toBeVisible();
  });

  test('resets history when selecting a different file', async ({ page }) => {
    // Mock entries
    await page.route('**/v1/files/entries**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            { name: 'file1.txt', path: 'file1.txt', type: 'ENTRY_TYPE_FILE', size: 10 },
            { name: 'file2.txt', path: 'file2.txt', type: 'ENTRY_TYPE_FILE', size: 20 },
          ],
        }),
      });
    });

    // Mock file contents
    await page.route('**/v1/files/file1.txt', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          file: { path: 'file1.txt', content: Buffer.from('File 1').toString('base64'), size: 6 },
        }),
      });
    });

    await page.route('**/v1/files/file2.txt', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          file: { path: 'file2.txt', content: Buffer.from('File 2').toString('base64'), size: 6 },
        }),
      });
    });

    // Mock history for file1
    await page.route('**/v1/files/history/file1.txt', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          changes: [
            {
              id: 'change-1',
              commit_hash: 'abc123',
              path: 'file1.txt',
              change_type: 'CHANGE_TYPE_ADD',
              author: 'user1',
              message: 'Add file1',
              timestamp: 1704067200,
            },
          ],
        }),
      });
    });

    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    // Select first file and toggle history
    await page.getByRole('button', { name: /file1\.txt/i }).click();
    await page.getByTestId('history-toggle').click();
    await expect(page.getByText('Add file1')).toBeVisible();

    // Select second file - should reset to content view
    await page.getByRole('button', { name: /file2\.txt/i }).click();
    await expect(page.getByText('File 2')).toBeVisible();
    await expect(page.getByTestId('history-panel')).not.toBeVisible();
    await expect(page.getByTestId('history-toggle')).toHaveText(/History/);
  });
});

test.describe('File History (Slice Mode)', () => {
  test('fetches history from slice-specific endpoint', async ({ page }) => {
    // Mock slice entries
    await page.route('**/v1/slices/my_slice/entries**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            { name: 'index.js', path: 'index.js', type: 'ENTRY_TYPE_FILE', size: 100 },
          ],
        }),
      });
    });

    // Mock file content
    await page.route('**/v1/slices/my_slice/files/index.js', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          file: {
            path: 'index.js',
            content: Buffer.from('export default {};').toString('base64'),
            size: 18,
          },
        }),
      });
    });

    // Mock slice-specific history
    await page.route('**/v1/slices/my_slice/files/history/index.js', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          changes: [
            {
              id: 'slice-change-1',
              slice_id: 'my_slice',
              commit_hash: 'slice123abc',
              path: 'index.js',
              change_type: 'CHANGE_TYPE_MODIFY',
              lines_added: 3,
              lines_deleted: 1,
              author: 'slice-dev@example.com',
              message: 'Update slice exports',
              timestamp: 1704153600,
            },
          ],
          has_more: false,
        }),
      });
    });

    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    // Switch to slice mode
    await page.locator('[data-testid="browse-mode"]').selectOption('slice');
    await page.locator('[data-testid="slice-id"]').fill('my_slice');

    // Select file
    await page.getByRole('button', { name: /index\.js/i }).click();
    await expect(page.getByText('export default {};')).toBeVisible();

    // Toggle history
    await page.getByTestId('history-toggle').click();

    // Verify slice-specific history is shown
    await expect(page.getByTestId('history-panel')).toBeVisible();
    await expect(page.getByText('Update slice exports')).toBeVisible();
    await expect(page.getByText('slice12')).toBeVisible();
    await expect(page.getByText('slice-dev@example.com')).toBeVisible();
  });
});

test.describe('File History - Change Types', () => {
  test('displays all change types with correct styling', async ({ page }) => {
    // Mock entries
    await page.route('**/v1/files/entries**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            { name: 'test.js', path: 'test.js', type: 'ENTRY_TYPE_FILE', size: 50 },
          ],
        }),
      });
    });

    // Mock file content
    await page.route('**/v1/files/test.js', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          file: { path: 'test.js', content: Buffer.from('test').toString('base64'), size: 4 },
        }),
      });
    });

    // Mock history with all change types
    await page.route('**/v1/files/history/test.js', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          changes: [
            {
              id: 'c1',
              commit_hash: 'hash1',
              change_type: 'CHANGE_TYPE_MODIFY',
              author: 'dev',
              message: 'Modified file',
              timestamp: 1704240000,
            },
            {
              id: 'c2',
              commit_hash: 'hash2',
              change_type: 'CHANGE_TYPE_RENAME',
              path: 'test.js',
              old_path: 'old-test.js',
              author: 'dev',
              message: 'Renamed file',
              timestamp: 1704153600,
            },
            {
              id: 'c3',
              commit_hash: 'hash3',
              change_type: 'CHANGE_TYPE_DELETE',
              author: 'dev',
              message: 'Deleted content',
              timestamp: 1704067200,
            },
            {
              id: 'c4',
              commit_hash: 'hash4',
              change_type: 'CHANGE_TYPE_ADD',
              author: 'dev',
              message: 'Added file',
              timestamp: 1703980800,
            },
          ],
        }),
      });
    });

    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();
    await page.getByRole('button', { name: /test\.js/i }).click();
    await page.getByTestId('history-toggle').click();

    // Verify all change types are displayed
    const changeTypes = page.locator('.change-type');
    await expect(changeTypes).toHaveCount(4);
    await expect(changeTypes).toContainText(['Modify', 'Rename', 'Delete', 'Add']);

    // Verify messages are shown
    await expect(page.getByText('Modified file')).toBeVisible();
    await expect(page.getByText('Renamed file')).toBeVisible();
    await expect(page.getByText('Deleted content')).toBeVisible();
    await expect(page.getByText('Added file')).toBeVisible();
  });

  test('handles numeric change type values', async ({ page }) => {
    // Mock entries
    await page.route('**/v1/files/entries**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            { name: 'numeric.txt', path: 'numeric.txt', type: 1, size: 10 },
          ],
        }),
      });
    });

    // Mock file content
    await page.route('**/v1/files/numeric.txt', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          file: { path: 'numeric.txt', content: Buffer.from('test').toString('base64'), size: 4 },
        }),
      });
    });

    // Mock history with numeric change types
    await page.route('**/v1/files/history/numeric.txt', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          changes: [
            { id: 'n1', commit_hash: 'h1', change_type: 1, author: 'dev', message: 'Add', timestamp: 1704067200 },
            { id: 'n2', commit_hash: 'h2', change_type: 2, author: 'dev', message: 'Modify', timestamp: 1703980800 },
          ],
        }),
      });
    });

    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();
    await page.getByRole('button', { name: /numeric\.txt/i }).click();
    await page.getByTestId('history-toggle').click();

    // Verify numeric types are correctly interpreted
    const numericChangeTypes = page.locator('.change-type');
    await expect(numericChangeTypes).toHaveCount(2);
    await expect(numericChangeTypes).toContainText(['Add', 'Modify']);
  });
});
