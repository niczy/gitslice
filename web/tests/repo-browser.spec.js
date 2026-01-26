import { test, expect } from '@playwright/test';

test.describe('Root Repository Browsing (path-first API)', () => {
  test('loads root entries without slice_id', async ({ page }) => {
    await page.route('**/v1/files/entries**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            { name: 'src', path: 'src', type: 'ENTRY_TYPE_DIRECTORY', size: 0, has_children: true },
            { name: 'README.md', path: 'README.md', type: 'ENTRY_TYPE_FILE', size: 100, has_children: false },
          ],
        }),
      });
    });

    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    // Verify root mode is default
    await expect(page.locator('[data-testid="browse-mode"]')).toHaveValue('root');
    await expect(page.getByRole('button', { name: /src/i })).toBeVisible();
    await expect(page.getByRole('button', { name: /README\.md/i })).toBeVisible();
  });

  test('loads root entries at specific commit', async ({ page }) => {
    await page.route('**/v1/files/entries?commit_hash=abc123**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            { name: 'old_file.txt', path: 'old_file.txt', type: 'ENTRY_TYPE_FILE', size: 50 },
          ],
        }),
      });
    });

    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    await page.locator('[data-testid="commit-hash"]').fill('abc123');

    // Wait for the entries to load
    await expect(page.getByRole('button', { name: /old_file\.txt/i })).toBeVisible();
  });

  test('fetches file from root without slice_id', async ({ page }) => {
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
    await page.route('**/v1/files/readme.md**', async (route) => {
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
    await page.getByRole('button', { name: /readme\.md/i }).click();
    await expect(page.getByText('# Hello World')).toBeVisible();
  });
});

test.describe('Slice-specific Browsing', () => {
  test('browses the repo tree and previews a file (slice mode)', async ({ page }) => {
    await page.route('**/v1/slices/test_slice/entries**', async (route, request) => {
      const url = new URL(request.url());
      const prefix = '/v1/slices/test_slice/entries';
      let path = url.pathname.startsWith(prefix) ? url.pathname.slice(prefix.length) : '';
      if (path.startsWith('/')) {
        path = path.slice(1);
      }
      path = decodeURIComponent(path);

      if (path === '') {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            entries: [
              { name: 'apps', path: 'apps', type: 'ENTRY_TYPE_DIRECTORY', size: 0, has_children: true },
              { name: 'docs', path: 'docs', type: 'ENTRY_TYPE_DIRECTORY', size: 0, has_children: true },
            ],
          }),
        });
        return;
      }

      if (path === 'apps') {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            entries: [
              { name: 'readme.md', path: 'apps/readme.md', type: 'ENTRY_TYPE_FILE', size: 18, has_children: false },
              { name: 'components', path: 'apps/components', type: 'ENTRY_TYPE_DIRECTORY', size: 0, has_children: true },
            ],
          }),
        });
        return;
      }

      if (path === 'apps/components') {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({
            entries: [{ name: 'button.jsx', path: 'apps/components/button.jsx', type: 'ENTRY_TYPE_FILE', size: 12 }],
          }),
        });
        return;
      }

      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ entries: [] }),
      });
    });

    await page.route('**/v1/slices/test_slice/files**', async (route, request) => {
      const url = new URL(request.url());
      const prefix = '/v1/slices/test_slice/files';
      let path = url.pathname.startsWith(prefix) ? url.pathname.slice(prefix.length) : '';
      if (path.startsWith('/')) {
        path = path.slice(1);
      }
      path = decodeURIComponent(path);
      const content = path === 'apps/readme.md' ? Buffer.from('# Hello\nPreview').toString('base64') : '';

      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          file: {
            path,
            content,
            size: content.length,
            hash: 'hash',
          },
        }),
      });
    });

    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    // Switch to slice mode
    await page.locator('[data-testid="browse-mode"]').selectOption('slice');

    // Enter slice ID
    await page.locator('[data-testid="slice-id"]').fill('test_slice');

    await expect(page.getByRole('heading', { name: /File tree/i })).toBeVisible();

    await page.getByRole('button', { name: /apps/i }).click();
    await expect(page.getByRole('button', { name: /readme\.md/i })).toBeVisible();

    await page.getByRole('button', { name: /readme\.md/i }).click();
    await expect(page.getByRole('heading', { name: /apps\/readme\.md/i })).toBeVisible();
    await expect(page.getByText('# Hello')).toBeVisible();
  });

  test('loads slice at specific version', async ({ page }) => {
    await page.route('**/v1/slices/my_slice/entries?slice_version.slice_hash=def456**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            { name: 'versioned_file.txt', path: 'versioned_file.txt', type: 'ENTRY_TYPE_FILE', size: 25 },
          ],
        }),
      });
    });

    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    // Switch to slice mode
    await page.locator('[data-testid="browse-mode"]').selectOption('slice');

    // Enter slice ID and hash
    await page.locator('[data-testid="slice-id"]').fill('my_slice');
    await page.locator('[data-testid="slice-hash"]').fill('def456');

    // Wait for the entries to load
    await expect(page.getByRole('button', { name: /versioned_file\.txt/i })).toBeVisible();
  });
});

// Legacy test for backwards compatibility
test('browses the repo tree and previews a file', async ({ page }) => {
  // This test uses the new path-first API (root mode)
  await page.route('**/v1/files/entries**', async (route, request) => {
    const url = new URL(request.url());
    const prefix = '/v1/files/entries';
    let path = url.pathname.startsWith(prefix) ? url.pathname.slice(prefix.length) : '';
    if (path.startsWith('/')) {
      path = path.slice(1);
    }
    path = decodeURIComponent(path);

    if (path === '') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            { name: 'apps', path: 'apps', type: 'ENTRY_TYPE_DIRECTORY', size: 0, has_children: true },
            { name: 'docs', path: 'docs', type: 'ENTRY_TYPE_DIRECTORY', size: 0, has_children: true },
          ],
        }),
      });
      return;
    }

    if (path === 'apps') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            { name: 'readme.md', path: 'apps/readme.md', type: 'ENTRY_TYPE_FILE', size: 18, has_children: false },
            { name: 'components', path: 'apps/components', type: 'ENTRY_TYPE_DIRECTORY', size: 0, has_children: true },
          ],
        }),
      });
      return;
    }

    if (path === 'apps/components') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [{ name: 'button.jsx', path: 'apps/components/button.jsx', type: 'ENTRY_TYPE_FILE', size: 12 }],
        }),
      });
      return;
    }

    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ entries: [] }),
    });
  });

  await page.route(/\/v1\/files\/(?!entries)/, async (route, request) => {
    const url = new URL(request.url());
    const prefix = '/v1/files';
    let path = url.pathname.startsWith(prefix) ? url.pathname.slice(prefix.length) : '';
    if (path.startsWith('/')) {
      path = path.slice(1);
    }
    path = decodeURIComponent(path);

    if (path === 'apps/readme.md') {
      const content = Buffer.from('# Hello\nPreview').toString('base64');
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          file: {
            path: 'apps/readme.md',
            content,
            size: content.length,
            hash: 'hash',
          },
        }),
      });
      return;
    }

    await route.fulfill({
      status: 404,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'File not found' }),
    });
  });

  await page.goto('/');
  await page.getByTestId('topbar-repo-browser').click();

  await expect(page.getByRole('heading', { name: /File tree/i })).toBeVisible();

  await page.getByRole('button', { name: /apps/i }).click();
  await expect(page.getByRole('button', { name: /readme\.md/i })).toBeVisible();

  await page.getByRole('button', { name: /readme\.md/i }).click();
  await expect(page.getByRole('heading', { name: /apps\/readme\.md/i })).toBeVisible();
  await expect(page.getByText('# Hello')).toBeVisible();
});
