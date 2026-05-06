import { test, expect } from '@playwright/test';

// Genesis populates files under o/genesis/projects/gitslice/
// so root entries should include the "o" directory.

async function openRootRepository(page) {
  await page.goto('/browser/root_slice');
  await expect(page.getByRole('heading', { name: /Root Slice/i })).toBeVisible();
  await expect(page.getByTestId('folder-preview')).toBeVisible();
}

async function openPreviewEntry(page, namePattern) {
  const entry = page.locator('.folder-preview-list').getByRole('button', { name: namePattern }).first();
  await expect(entry).toBeVisible();
  await entry.click();
}

async function openGitsliceRepositoryRoot(page) {
  await openPreviewEntry(page, /^o\b/i);
  await expect(page.getByRole('heading', { name: /^o$/i })).toBeVisible();
  await openPreviewEntry(page, /^genesis\b/i);
  await expect(page.getByRole('heading', { name: /^genesis$/i })).toBeVisible();
  await openPreviewEntry(page, /^projects\b/i);
  await expect(page.getByRole('heading', { name: /^projects$/i })).toBeVisible();
  await openPreviewEntry(page, /^gitslice\b/i);
  await expect(page.getByRole('heading', { name: /^gitslice$/i })).toBeVisible();
}

test.describe('Root Repository Browsing (real server)', () => {
  test('loads root entries and shows genesis directory tree', async ({ page }) => {
    await openRootRepository(page);

    // The navigator should render the full folder path, even at the root level.
    await expect(page.locator('.folder-preview-list').getByRole('button', { name: /^o\b/i })).toBeVisible();
  });

  test('navigates into genesis directory and finds repo files', async ({ page }) => {
    await openRootRepository(page);
    await openGitsliceRepositoryRoot(page);

    // Should see real repo files
    await expect(page.locator('.folder-preview-list').getByRole('button', { name: /README\.md/i })).toBeVisible();
    await expect(page.locator('.folder-preview-list').getByRole('button', { name: /go\.mod/i })).toBeVisible();
    await expect(page.locator('.folder-preview-list').getByRole('button', { name: /Makefile/i })).toBeVisible();
  });

  test('previews a real file from the genesis repository', async ({ page }) => {
    await openRootRepository(page);
    await openGitsliceRepositoryRoot(page);

    // Click README.md to preview it
    await openPreviewEntry(page, /^README\.md\b/i);

    // The code header breadcrumbs should include the selected file.
    await expect(page.locator('.code-header .breadcrumb').filter({ hasText: /README\.md/i })).toBeVisible();

    // The real README.md should contain some content (it's a gitslice project)
    // Check for common markdown content that should be in the file
    const preview = page.locator('.file-preview');
    await expect(preview).toBeVisible();
    await expect(preview).not.toBeEmpty();
  });

  test('navigates into subdirectories and back', async ({ page }) => {
    await openRootRepository(page);
    await openGitsliceRepositoryRoot(page);

    // Navigate into internal/ subdirectory
    await openPreviewEntry(page, /^internal\b/i);

    // Should see child entries under internal (type classification varies between runs)
    await expect(page.locator('.folder-preview-list').getByRole('button', { name: /common|config|models|services|storage/i }).first()).toBeVisible();
  });


  test('keeps selected file open after refresh', async ({ page }) => {
    await openRootRepository(page);
    await openGitsliceRepositoryRoot(page);

    await openPreviewEntry(page, /^README\.md\b/i);
    await expect(page.locator('.code-header .breadcrumb').filter({ hasText: /README\.md/i })).toBeVisible();

    await page.reload();

    await expect(page.locator('.code-header .breadcrumb').filter({ hasText: /README\.md/i })).toBeVisible();
  });
  test('shows file sizes in the entry list', async ({ page }) => {
    await openRootRepository(page);
    await openGitsliceRepositoryRoot(page);

    // README.md should have a file size displayed (any valid size format)
    const readmeBtn = page.locator('.folder-preview-list').getByRole('button', { name: /README\.md/i });
    await expect(readmeBtn).toBeVisible();
    // Size should contain B, KB, or MB
    await expect(readmeBtn).toContainText(/\d+(\.\d+)?\s*(B|KB|MB)/);
  });
});

test.describe('Slice-specific Browsing (real server)', () => {
  test('browses root_slice in slice mode', async ({ page }) => {
    await openRootRepository(page);

    await expect(page.getByRole('heading', { name: /File tree/i })).toBeVisible();

    // Should see the "o" directory (genesis files)
    await expect(page.locator('.folder-preview-list').getByRole('button', { name: /^o\b/i })).toBeVisible();
  });
});

test.describe('Repo Browser File Preview Layout', () => {
  test('shows checkout first in Get Code and copies both commands', async ({ page }) => {
    const username = `cloneuser${Date.now()}`;
    const sliceId = `home.${username}`;
    const slug = `${username}/demo-slice`;

    await page.addInitScript(() => {
      window.__copiedTexts = [];
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: {
          writeText: async (text) => {
            window.__copiedTexts.push(text);
          },
        },
      });
    });

    await page.route('**/v1/slices?limit=200', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          slices: [
            {
              slice_id: sliceId,
              name: 'Demo Slice',
              slug,
              description: 'Clone command test slice',
              owners: [username],
              created_by: username,
              is_root: false,
              file_count: 1,
            },
          ],
        }),
      });
    });

    await page.route(`**/v1/slices/${sliceId}/entries**`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            {
              id: `${sliceId}:README.md`,
              name: 'README.md',
              path: 'README.md',
              type: 'FILE',
              size: 92,
            },
          ],
        }),
      });
    });

    await page.goto('/login');
    await page.getByLabel('Username').fill(username);
    await page.getByRole('button', { name: /login with username/i }).click();
    await page.goto(`/browser/${sliceId}`);

    await expect(page.getByTestId('slice-get-code-button')).toBeVisible();
    await page.getByTestId('slice-get-code-button').click();

    const checkoutCommand = page.getByTestId('slice-get-code-command');
    await expect(checkoutCommand).toHaveText(`gs slice checkout ${slug}`);

    const gitCommand = page.getByTestId('slice-get-code-git-command');
    const expectedApiBaseUrl = process.env.E2E_API_BASE_URL || `http://127.0.0.1:${process.env.E2E_CORE_PORT || process.env.E2E_GATEWAY_PORT || '50151'}`;
    await expect(gitCommand).toHaveText(`git clone ${expectedApiBaseUrl}/git/${slug}.git`);

    await page.getByTestId('slice-get-code-copy').click();
    await expect(page.locator('.slice-detail-get-code-note')).toContainText('Copied checkout command.');
    await page.getByTestId('slice-get-code-git-copy').click();
    await expect(page.locator('.slice-detail-get-code-note')).toContainText('Copied Git clone command.');

    const copiedTexts = await page.evaluate(() => window.__copiedTexts);
    expect(copiedTexts).toEqual([
      await checkoutCommand.textContent(),
      await gitCommand.textContent(),
    ]);
  });

  test('renders markdown previews at full code content width', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });

    await page.route('**/v1/slices?limit=200', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          slices: [
            {
              slice_id: 'root_slice',
              name: 'Root Slice',
              description: 'Root slice',
              owners: ['system'],
              created_by: 'system',
              is_root: true,
              file_count: 1,
            },
          ],
        }),
      });
    });

    await page.route('**/v1/slices/root_slice/entries**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            {
              id: 'root_slice:README.md',
              name: 'README.md',
              path: 'README.md',
              type: 'FILE',
              size: 92,
            },
          ],
        }),
      });
    });

    await page.route('**/v1/slices/root_slice/files/README.md**', async (route) => {
      const content = '# Wide markdown preview\n\nThis markdown file should use the available preview width.';
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          file: {
            path: 'README.md',
            content: Buffer.from(content, 'utf8').toString('base64'),
          },
        }),
      });
    });

    await page.goto('/browser/root_slice?file=README.md');

    const preview = page.locator('.file-preview-markdown');
    await expect(preview).toContainText('Wide markdown preview');

    const previewLayout = await preview.evaluate((element) => {
      const content = element.closest('.code-content');
      const previewStyle = window.getComputedStyle(element);
      const contentStyle = window.getComputedStyle(content);
      const horizontalPadding = parseFloat(contentStyle.paddingLeft) + parseFloat(contentStyle.paddingRight);
      return {
        boxSizing: previewStyle.boxSizing,
        maxWidth: previewStyle.maxWidth,
        previewWidth: element.getBoundingClientRect().width,
        contentWidth: content.getBoundingClientRect().width - horizontalPadding,
      };
    });

    expect(previewLayout.boxSizing).toBe('border-box');
    expect(previewLayout.maxWidth).toBe('none');
    expect(previewLayout.previewWidth).toBeGreaterThanOrEqual(previewLayout.contentWidth - 1);
    expect(previewLayout.previewWidth).toBeLessThanOrEqual(previewLayout.contentWidth + 1);
  });

  test('keeps long mobile file breadcrumbs from overlapping', async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });

    const filePath = [
      'workspaces',
      'extremely-long-customer-folder-name',
      'deeply-nested-product-area',
      'ridiculously-long-component-file-name-for-mobile-layout.jsx',
    ].join('/');

    await page.route('**/v1/slices?limit=200', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          slices: [
            {
              slice_id: 'root_slice',
              name: 'Root Slice',
              description: 'Root slice',
              owners: ['system'],
              created_by: 'system',
              is_root: true,
              file_count: 1,
            },
          ],
        }),
      });
    });

    await page.route('**/v1/slices/root_slice/entries**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            {
              id: 'root_slice:workspaces',
              name: 'workspaces',
              path: 'workspaces',
              type: 'ENTRY_TYPE_DIRECTORY',
              size: 0,
            },
          ],
        }),
      });
    });

    await page.route('**/v1/slices/root_slice/files/**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          file: {
            path: filePath,
            content: Buffer.from('export default function MobilePathFixture() {}', 'utf8').toString('base64'),
          },
        }),
      });
    });

    await page.goto(`/browser/root_slice?file=${encodeURIComponent(filePath)}`);
    await expect(page.locator('.code-header .breadcrumb')).toHaveCount(3);

    const layout = await page.locator('.code-header').evaluate((header) => {
      const headerRect = header.getBoundingClientRect();
      const breadcrumbRects = Array.from(header.querySelectorAll('.breadcrumb')).map((item) => {
        const rect = item.getBoundingClientRect();
        return {
          left: rect.left,
          right: rect.right,
          top: rect.top,
          bottom: rect.bottom,
        };
      });
      return {
        headerLeft: headerRect.left,
        headerRight: headerRect.right,
        breadcrumbRects,
      };
    });

    for (let index = 0; index < layout.breadcrumbRects.length; index += 1) {
      const rect = layout.breadcrumbRects[index];
      expect(rect.left).toBeGreaterThanOrEqual(layout.headerLeft - 1);
      expect(rect.right).toBeLessThanOrEqual(layout.headerRight + 1);
      if (index > 0) {
        const previous = layout.breadcrumbRects[index - 1];
        const sameLine = Math.abs(previous.top - rect.top) < 1 && Math.abs(previous.bottom - rect.bottom) < 1;
        if (sameLine) {
          expect(previous.right).toBeLessThanOrEqual(rect.left + 1);
        }
      }
    }
  });

  test('adds file and directory selections to browser history', async ({ page }) => {
    await page.goto('/browser/root_slice');
    await expect(page.getByRole('heading', { name: /Root Slice/i })).toBeVisible();

    await openPreviewEntry(page, /^o\b/i);
    await expect(page).toHaveURL(/\/browser\/root_slice\?dir=o$/);
    await expect(page.getByRole('heading', { name: /^o$/i })).toBeVisible();

    await openPreviewEntry(page, /^genesis\b/i);
    await expect(page).toHaveURL(/\/browser\/root_slice\?dir=o%2Fgenesis$/);
    await expect(page.getByRole('heading', { name: /^genesis$/i })).toBeVisible();

    await openPreviewEntry(page, /^projects\b/i);
    await expect(page).toHaveURL(/\/browser\/root_slice\?dir=o%2Fgenesis%2Fprojects$/);
    await expect(page.getByRole('heading', { name: /^projects$/i })).toBeVisible();

    await openPreviewEntry(page, /^gitslice\b/i);
    await expect(page).toHaveURL(/\/browser\/root_slice\?dir=o%2Fgenesis%2Fprojects%2Fgitslice$/);
    await expect(page.getByRole('heading', { name: /^gitslice$/i })).toBeVisible();

    await openPreviewEntry(page, /^README\.md\b/i);
    await expect(page).toHaveURL(/\/browser\/root_slice\?file=o%2Fgenesis%2Fprojects%2Fgitslice%2FREADME\.md$/);
    await expect(page.locator('.code-header .breadcrumb').filter({ hasText: /README\.md/i })).toBeVisible();

    await page.goBack();
    await expect(page).toHaveURL(/\/browser\/root_slice\?dir=o%2Fgenesis%2Fprojects%2Fgitslice$/);
    await expect(page.getByRole('heading', { name: /^gitslice$/i })).toBeVisible();
    await expect(page.locator('.code-header .breadcrumb').filter({ hasText: /README\.md/i })).toHaveCount(0);

    await page.goBack();
    await expect(page).toHaveURL(/\/browser\/root_slice\?dir=o%2Fgenesis%2Fprojects$/);
    await expect(page.getByRole('heading', { name: /^projects$/i })).toBeVisible();
  });
});

test.describe('Repo Browser Search', () => {
  test('submits indexed workspace search and renders structured results', async ({ page }) => {
    const username = `zzsrch${Date.now()}`;

    await page.goto('/login');
    await page.getByLabel('Username').fill(username);
    await page.getByRole('button', { name: /login with username/i }).click();

    await expect(page).toHaveURL(/\/browser(\?.*)?$/);
    await page.getByTestId('slice-home-row').first().click();
    await expect(page).toHaveURL(new RegExp(`/browser/home\\.${username}`));
    await expect(page.getByTestId('repo-search-panel')).toBeVisible();

    let sawSearchRequest = false;
    await page.route('**/v1/fs/workspaces/*:search**', async (route) => {
      const url = new URL(route.request().url());
      expect(url.searchParams.get('query')).toBe('TODO:\\s+ship search');
      expect(url.searchParams.get('glob')).toBe('/' + username + '/notes/*.md');
      expect(url.searchParams.get('regex')).toBe('true');
      sawSearchRequest = true;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          workspace_id: `home.${username}`,
          query: 'TODO:\\s+ship search',
          glob: `/${username}/notes/*.md`,
          matches: [
            {
              path: `/${username}/notes/todo.md`,
              line_number: 7,
              line: 'TODO: ship search',
            },
          ],
        }),
      });
    });

    await page.getByTestId('repo-search-query').fill('TODO:\\s+ship search');
    await page.getByTestId('repo-search-glob').fill(`/${username}/notes/*.md`);
    await page.getByTestId('repo-search-regex').check();
    await page.getByTestId('repo-search-submit').click();

    await expect(page.getByTestId('repo-search-results')).toBeVisible();
    await expect(page.getByTestId('repo-search-result')).toContainText(`/${username}/notes/todo.md`);
    await expect(page.getByTestId('repo-search-result')).toContainText('Line 7');
    await expect(page.getByTestId('repo-search-result')).toContainText('TODO: ship search');
    expect(sawSearchRequest).toBe(true);
  });
});

test.describe('Repo Browser Mobile Navigation', () => {
  test('keeps the file tree drawer open while expanding folders', async ({ page }) => {
    const username = `mobiletree${Date.now()}`;
    const sliceId = `home.${username}`;

    await page.setViewportSize({ width: 390, height: 844 });

    await page.route('**/v1/slices?limit=200', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          slices: [
            {
              slice_id: sliceId,
              name: sliceId,
              description: 'Mobile tree test slice',
              files: ['docs/guide.md'],
              owners: [username],
              created_by: username,
              is_root: false,
            },
          ],
        }),
      });
    });

    await page.route(`**/v1/slices/${sliceId}/entries**`, async (route) => {
      const requestUrl = new URL(route.request().url());
      const decodedPath = decodeURIComponent(requestUrl.pathname.split('/entries')[1] || '').replace(/^\/+/, '');
      const rootFolderPath = username;
      const docsFolderPath = `${username}/docs`;
      const entries = decodedPath === docsFolderPath
        ? [
            {
              id: `${sliceId}:${docsFolderPath}/guide.md`,
              name: 'guide.md',
              path: `${docsFolderPath}/guide.md`,
              type: 'ENTRY_TYPE_FILE',
              size: 18,
            },
          ]
        : decodedPath === rootFolderPath
          ? [
              {
                id: `${sliceId}:${docsFolderPath}`,
                name: 'docs',
                path: docsFolderPath,
                type: 'ENTRY_TYPE_DIRECTORY',
                size: 0,
              },
            ]
        : [
            {
              id: `${sliceId}:${rootFolderPath}`,
              name: username,
              path: rootFolderPath,
              type: 'ENTRY_TYPE_DIRECTORY',
              size: 0,
            },
          ];

      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ entries }),
      });
    });

    await page.goto('/login');
    await page.getByLabel('Username').fill(username);
    await page.getByRole('button', { name: /login with username/i }).click();

    await expect(page).toHaveURL(/\/browser(\?.*)?$/);
    await page.getByTestId('slice-home-row').first().click();
    await expect(page).toHaveURL(new RegExp(`/browser/${sliceId.replace('.', '\\.')}`));

    await expect(page.getByTestId('sidebar-toggle')).toBeVisible();
    await page.getByTestId('sidebar-toggle').click();

    const sidebar = page.locator('.repo-sidebar');
    await expect(sidebar).toHaveClass(/open/);
    await expect(sidebar).toHaveCSS('position', 'fixed');
    const transitionProperty = await sidebar.evaluate((element) => getComputedStyle(element).transitionProperty);
    expect(transitionProperty).toContain('transform');

    await sidebar.getByRole('button', { name: new RegExp(username) }).click();
    await expect(sidebar).toHaveClass(/open/);
    await expect(sidebar.getByRole('button', { name: /docs/i })).toBeVisible();

    await sidebar.getByRole('button', { name: /docs/i }).click();

    await expect(sidebar).toHaveClass(/open/);
    await expect(sidebar.getByRole('button', { name: /guide\.md/i })).toBeVisible();

    const overlay = page.locator('.sidebar-overlay');
    await sidebar.getByRole('button', { name: /close sidebar/i }).click();
    await expect(sidebar).toHaveClass(/closed/);
    await expect(sidebar).toHaveClass(/dismissing/);
    await expect(overlay).toHaveClass(/visible/);
    await expect(overlay).toHaveClass(/dismissing/);
    await expect(sidebar).not.toHaveClass(/dismissing/, { timeout: 1000 });
    await expect(overlay).not.toHaveClass(/visible/, { timeout: 1000 });
  });
});

test.describe('Slice Activity Pages', () => {
  test('shows slice commits and links to the commit diff view', async ({ page }) => {
    const username = `activity${Date.now()}`;
    const sliceId = `home.${username}`;
    const commitHash = 'fs-activity-commit-1';

    await page.route('**/v1/slices?limit=200', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          slices: [
            {
              slice_id: sliceId,
              name: sliceId,
              description: 'Activity test slice',
              owners: [username],
              created_by: username,
              is_root: false,
              file_count: 3,
            },
          ],
        }),
      });
    });

    await page.route(`**/v1/slices/${sliceId}/commits**`, async (route) => {
      const url = new URL(route.request().url());
      expect(url.searchParams.get('limit')).toBe('100');
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          commits: [
            {
              commitHash,
              parentHash: 'fs-parent',
              timestamp: 1777955400,
              message: 'upload docs directory',
            },
            {
              commitHash: 'fs-parent',
              parentHash: '',
              timestamp: 1777955300,
              message: 'initial workspace',
            },
          ],
        }),
      });
    });

    await page.route(`**/v1/commits/${commitHash}/changes`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          commit_hash: commitHash,
          files_added: 1,
          files_modified: 0,
          files_deleted: 0,
          changes: [
            {
              id: 'change-1',
              path: `${username}/docs/readme.md`,
              change_type: 'add',
              lines_added: 2,
              lines_deleted: 0,
              patch: '--- /dev/null\n+++ b/readme.md\n@@\n+hello\n+world',
            },
          ],
        }),
      });
    });

    await page.goto('/login');
    await page.getByLabel('Username').fill(username);
    await page.getByRole('button', { name: /login with username/i }).click();
    await page.goto(`/browser/${sliceId}/commits`);

    await expect(page.getByTestId('slice-commits-page')).toBeVisible();
    await expect(page.getByTestId('slice-detail-tab-commits')).toHaveAttribute('aria-selected', 'true');
    await expect(page.getByTestId('slice-commit-row').first()).toContainText('upload docs directory');
    await expect(page.getByTestId('slice-commit-row').first()).toContainText(commitHash.slice(0, 12));

    await page.getByTestId('slice-commit-row').first().click();
    await expect(page).toHaveURL(new RegExp(`/diff/${commitHash}`));
    await expect(page.getByTestId('commit-diff-page')).toBeVisible();
  });

  test('shows slice changesets with status filters and links to changeset diff', async ({ page }) => {
    const username = `activitycs${Date.now()}`;
    const sliceId = `home.${username}`;
    const requests = [];

    await page.route('**/v1/slices?limit=200', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          slices: [
            {
              slice_id: sliceId,
              name: sliceId,
              description: 'Changeset activity test slice',
              owners: [username],
              created_by: username,
              is_root: false,
              file_count: 2,
            },
          ],
        }),
      });
    });

    await page.route(`**/v1/slices/${sliceId}/changesets**`, async (route) => {
      const url = new URL(route.request().url());
      const statusFilter = url.searchParams.get('status_filter');
      requests.push(statusFilter || `all:${url.searchParams.get('include_all_statuses')}`);
      const allChangesets = [
        {
          changesetId: 'cs-pending-review',
          changesetHash: 'hash-pending',
          sliceId,
          baseCommitHash: 'fs-base-1',
          modifiedFiles: [`${username}/todo.md`],
          status: 'PENDING',
          author: username,
          createdAt: 1777955400,
          message: 'draft todo update',
        },
        {
          changesetId: 'cs-merged-review',
          changesetHash: 'hash-merged',
          sliceId,
          baseCommitHash: 'fs-base-2',
          modifiedFiles: [`${username}/done.md`, `${username}/notes.md`],
          status: 'MERGED',
          author: username,
          createdAt: 1777955300,
          mergedAt: 1777955350,
          message: 'merged notes update',
        },
      ];
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          changesets: statusFilter === '3' ? allChangesets.slice(1) : allChangesets,
        }),
      });
    });

    await page.route('**/v1/changesets/cs-merged-review/snapshots**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ snapshots: [] }),
      });
    });

    await page.route('**/v1/changesets/cs-merged-review/diff**', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          changeset: {
            changesetId: 'cs-merged-review',
            sliceId,
            status: 'MERGED',
            author: username,
            createdAt: 1777955300,
            message: 'merged notes update',
          },
          diff: { filesAdded: 0, filesModified: 1, filesDeleted: 0 },
          changes: [],
        }),
      });
    });

    await page.goto('/login');
    await page.getByLabel('Username').fill(username);
    await page.getByRole('button', { name: /login with username/i }).click();
    await page.goto(`/browser/${sliceId}/changesets`);

    await expect(page.getByTestId('slice-changesets-page')).toBeVisible();
    await expect(page.getByTestId('slice-detail-tab-changesets')).toHaveAttribute('aria-selected', 'true');
    await expect(page.getByTestId('slice-changeset-row')).toHaveCount(2);
    expect(requests).toContain('all:true');

    await page.getByTestId('changeset-filter-merged').click();
    await expect(page.getByTestId('slice-changeset-row')).toHaveCount(1);
    await expect(page.getByTestId('slice-changeset-row').first()).toContainText('merged notes update');
    expect(requests).toContain('3');

    await page.getByTestId('slice-changeset-row').first().click();
    await expect(page).toHaveURL(/\/changesets\/cs-merged-review/);
    await expect(page.getByTestId('changeset-diff-page')).toBeVisible();
  });
});

test.describe('Repo Browser Settings', () => {
  test('manages slice visibility with public links', async ({ page }) => {
    const username = `webvisibility${Date.now()}`;
    const sliceId = `home.${username}`;
    const sliceSetBodies = [];
    let sliceVisibility = 1;

    await page.addInitScript(() => {
      window.__copiedTexts = [];
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: {
          writeText: async (text) => {
            window.__copiedTexts.push(text);
          },
        },
      });
    });

    await page.route('**/v1/slices?limit=200', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          slices: [
            {
              slice_id: sliceId,
              name: sliceId,
              description: 'Visibility test slice',
              files: ['docs/guide.md'],
              owners: [username],
              created_by: username,
              is_root: false,
            },
          ],
        }),
      });
    });

    await page.route('**/v1/environments?limit=500&offset=0', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          environments: [
            { name: 'staging', displayName: 'Staging' },
          ],
        }),
      });
    });

    await page.route(`**/v1/slices/${sliceId}/environment`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          slice_id: sliceId,
          environment: '',
        }),
      });
    });

    await page.route(`**/v1/slices/${sliceId}/visibility`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          slice_id: sliceId,
          visibility: sliceVisibility,
          path_propagation_mode: 1,
        }),
      });
    });

    await page.route(`**/v1/slices/${sliceId}:setVisibility`, async (route) => {
      const payload = JSON.parse(route.request().postData() || '{}');
      sliceSetBodies.push(payload);
      sliceVisibility = payload.visibility;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          slice_id: sliceId,
          visibility: sliceVisibility,
          path_propagation_mode: payload.pathPropagationMode,
        }),
      });
    });

    await page.goto('/login');
    await page.getByLabel('Username').fill(username);
    await page.getByRole('button', { name: /login with username/i }).click();

    await expect(page).toHaveURL(/\/browser(\?.*)?$/);
    await page.getByTestId('slice-home-row').first().click();
    await expect(page).toHaveURL(new RegExp(`/browser/${sliceId.replace('.', '\\.')}`));

    await page.getByTestId('repo-view-settings').click();
    await expect(page.getByTestId('slice-settings-panel')).toBeVisible();
    await expect(page.getByTestId('slice-visibility-status')).toContainText('Private');

    await page.getByTestId('slice-visibility-propagation').selectOption('public');
    await page.getByTestId('slice-visibility-set-public').click();
    await expect(page.getByTestId('slice-visibility-status')).toContainText('Public');
    expect(sliceSetBodies).toEqual([
      {
        visibility: 2,
        pathPropagationMode: 2,
      },
    ]);

    const origin = new URL(page.url()).origin;
    const sliceUrl = `${origin}/v1/public/entries?slice_id=${sliceId}`;

    await expect(page.getByTestId('slice-visibility-url')).toHaveValue(sliceUrl);

    await page.getByTestId('slice-visibility-copy-url').click();

    await expect(page.getByTestId('slice-visibility-copy-url')).toContainText('Copy public URL');
    const copiedTexts = await page.evaluate(() => window.__copiedTexts);
    expect(copiedTexts).toEqual([sliceUrl]);
  });
});
