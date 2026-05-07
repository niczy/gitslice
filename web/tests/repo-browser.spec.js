import { test, expect } from '@playwright/test';

// Genesis populates files under o/genesis/projects/gitslice/
// so root entries should include the "o" directory.

async function openRootRepository(page) {
  await page.goto('/slices/root_slice');
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

  test('opens the containing folder from the selected file breadcrumb', async ({ page }) => {
    await openRootRepository(page);
    await openGitsliceRepositoryRoot(page);

    await openPreviewEntry(page, /^README\.md\b/i);
    const fileBreadcrumb = page.locator('.code-header .breadcrumb').filter({ hasText: /README\.md/i });
    await expect(fileBreadcrumb).toBeVisible();

    await fileBreadcrumb.click();

    await expect(page).toHaveURL(/\/slices\/root_slice\?dir=o%2Fgenesis%2Fprojects%2Fgitslice$/);
    await expect(page.getByTestId('folder-preview')).toBeVisible();
    await expect(page.getByRole('heading', { name: /^gitslice$/i })).toBeVisible();
    await expect(page.locator('.code-header .breadcrumb').filter({ hasText: /README\.md/i })).toHaveCount(0);
    await expect(page.locator('.code-content .panel-error')).toHaveCount(0);
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

  test('shows not found for signed-out private slice direct URLs', async ({ page }) => {
    const response = await page.goto('/slices/sl-private-not-visible?file=secret.txt');

    expect(response?.status()).toBe(404);
    await expect(page.getByTestId('not-found-page')).toBeVisible();
    await expect(page.getByRole('heading', { name: /Page not found/i })).toBeVisible();
    await expect(page.getByTestId('slice-detail-nav')).toHaveCount(0);
    await expect(page.locator('.folder-preview-list')).toHaveCount(0);
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
    await page.goto(`/slices/${sliceId}`);

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

    await page.goto('/slices/root_slice?file=README.md');

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

  test('uses the tree loading indicator while files load', async ({ page }) => {
    await openRootRepository(page);
    await openGitsliceRepositoryRoot(page);

    await page.route('**/v1/slices/root_slice/files/**/README.md**', async (route) => {
      await page.waitForTimeout(700);
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          file: {
            path: 'README.md',
            size: 1234,
            content: Buffer.from('Subtle loading fixture', 'utf8').toString('base64'),
          },
        }),
      });
    });

    const treeLoading = page.getByTestId('tree-loading-indicator');
    await page.locator('.folder-preview-list').getByRole('button', { name: /^README\.md\b/i }).click();

    const fileSizeStatus = page.locator('.code-header-actions .file-size-status');
    await expect(fileSizeStatus).toBeVisible();
    await expect(fileSizeStatus).not.toHaveText('0 B');
    const loadingSizeWidth = await fileSizeStatus.evaluate((element) => element.getBoundingClientRect().width);

    await expect(treeLoading).toHaveClass(/visible/);
    await expect(treeLoading).toHaveAttribute('aria-label', 'Loading repository content');
    await expect(page.getByTestId('file-loading-state')).toHaveCount(0);

    await expect(page.locator('.file-preview')).toContainText('Subtle loading fixture');
    await expect(treeLoading).not.toHaveClass(/visible/);
    await expect(fileSizeStatus).toHaveText('1.2 KB');
    const loadedSizeWidth = await fileSizeStatus.evaluate((element) => element.getBoundingClientRect().width);
    expect(Math.abs(loadedSizeWidth - loadingSizeWidth)).toBeLessThan(1);

    await page.route('**/v1/slices/root_slice/files/**/go.mod**', async (route) => {
      await page.waitForTimeout(700);
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          file: {
            path: 'go.mod',
            size: 2048,
            content: Buffer.from('module delayed-preview-fixture', 'utf8').toString('base64'),
          },
        }),
      });
    });

    await page.evaluate(() => {
      const file = 'o/genesis/projects/gitslice/go.mod';
      const state = { gitsliceBrowserState: true, browserState: { slice: 'root_slice', file } };
      window.history.pushState(state, '', `/slices/root_slice?file=${encodeURIComponent(file)}`);
      window.dispatchEvent(new PopStateEvent('popstate', { state }));
    });
    await page.waitForTimeout(100);
    const previewTextDuringLoad = await page.locator('.file-preview').textContent();
    expect(previewTextDuringLoad.trim().length).toBeGreaterThan(0);
    expect(previewTextDuringLoad).not.toContain('File is empty.');
    await page.waitForTimeout(250);
    await expect(treeLoading).toHaveClass(/visible/);
    await expect(page.getByTestId('file-loading-state')).toHaveCount(0);
    await expect(page.locator('.file-preview')).toContainText('module delayed-preview-fixture');
    await expect(page.getByTestId('file-loading-state')).toHaveCount(0);
    await expect(treeLoading).not.toHaveClass(/visible/);
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

    await page.goto(`/slices/root_slice?file=${encodeURIComponent(filePath)}`);
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
    await page.goto('/slices/root_slice');
    await expect(page.getByRole('heading', { name: /Root Slice/i })).toBeVisible();

    await openPreviewEntry(page, /^o\b/i);
    await expect(page).toHaveURL(/\/slices\/root_slice\?dir=o$/);
    await expect(page.getByRole('heading', { name: /^o$/i })).toBeVisible();

    await openPreviewEntry(page, /^genesis\b/i);
    await expect(page).toHaveURL(/\/slices\/root_slice\?dir=o%2Fgenesis$/);
    await expect(page.getByRole('heading', { name: /^genesis$/i })).toBeVisible();

    await openPreviewEntry(page, /^projects\b/i);
    await expect(page).toHaveURL(/\/slices\/root_slice\?dir=o%2Fgenesis%2Fprojects$/);
    await expect(page.getByRole('heading', { name: /^projects$/i })).toBeVisible();

    await openPreviewEntry(page, /^gitslice\b/i);
    await expect(page).toHaveURL(/\/slices\/root_slice\?dir=o%2Fgenesis%2Fprojects%2Fgitslice$/);
    await expect(page.getByRole('heading', { name: /^gitslice$/i })).toBeVisible();

    await openPreviewEntry(page, /^README\.md\b/i);
    await expect(page).toHaveURL(/\/slices\/root_slice\?file=o%2Fgenesis%2Fprojects%2Fgitslice%2FREADME\.md$/);
    await expect(page.locator('.code-header .breadcrumb').filter({ hasText: /README\.md/i })).toBeVisible();

    await page.goBack();
    await expect(page).toHaveURL(/\/slices\/root_slice\?dir=o%2Fgenesis%2Fprojects%2Fgitslice$/);
    await expect(page.getByRole('heading', { name: /^gitslice$/i })).toBeVisible();
    await expect(page.locator('.code-header .breadcrumb').filter({ hasText: /README\.md/i })).toHaveCount(0);

    await page.goBack();
    await expect(page).toHaveURL(/\/slices\/root_slice\?dir=o%2Fgenesis%2Fprojects$/);
    await expect(page.getByRole('heading', { name: /^projects$/i })).toBeVisible();
  });

  test('adds sidebar tree file and directory selections to browser history state', async ({ page }) => {
    await page.goto('/slices/root_slice');
    await expect(page.getByRole('heading', { name: /Root Slice/i })).toBeVisible();

    const sidebar = page.locator('.repo-sidebar');

    await sidebar.getByRole('button', { name: /^o\b/i }).click();
    await expect(page).toHaveURL(/\/slices\/root_slice\?dir=o$/);
    await expect(page.getByRole('heading', { name: /^o$/i })).toBeVisible();
    await expect.poll(
      () => page.evaluate(() => window.history.state?.browserState),
    ).toMatchObject({
      dir: 'o',
      file: '',
      slice: 'root_slice',
    });

    await sidebar.getByRole('button', { name: /^genesis\b/i }).click();
    await expect(page).toHaveURL(/\/slices\/root_slice\?dir=o%2Fgenesis$/);
    await sidebar.getByRole('button', { name: /^projects\b/i }).click();
    await expect(page).toHaveURL(/\/slices\/root_slice\?dir=o%2Fgenesis%2Fprojects$/);
    await sidebar.getByRole('button', { name: /^gitslice\b/i }).click();
    await expect(page).toHaveURL(/\/slices\/root_slice\?dir=o%2Fgenesis%2Fprojects%2Fgitslice$/);

    await sidebar.getByRole('button', { name: /^README\.md\b/i }).click();
    await expect(page).toHaveURL(/\/slices\/root_slice\?file=o%2Fgenesis%2Fprojects%2Fgitslice%2FREADME\.md$/);
    await expect(page.locator('.code-header .breadcrumb').filter({ hasText: /README\.md/i })).toBeVisible();
    await expect.poll(
      () => page.evaluate(() => window.history.state?.browserState),
    ).toMatchObject({
      dir: '',
      file: 'o/genesis/projects/gitslice/README.md',
      slice: 'root_slice',
    });

    await page.goBack();
    await expect(page).toHaveURL(/\/slices\/root_slice\?dir=o%2Fgenesis%2Fprojects%2Fgitslice$/);
    await expect(page.getByRole('heading', { name: /^gitslice$/i })).toBeVisible();
    await expect(page.locator('.code-header .breadcrumb').filter({ hasText: /README\.md/i })).toHaveCount(0);
  });
});

test.describe('Slice Home Page', () => {
  test('shows visibility chips without a leading workspace icon', async ({ page }) => {
    const username = `homeicons${Date.now()}`;
    const privateSliceId = `home.${username}`;
    const publicSliceId = `${username}.public`;

    await page.route('**/v1/slices?limit=200', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          slices: [
            {
              slice_id: privateSliceId,
              name: 'Private Demo',
              slug: `${username}/private-demo`,
              created_by: username,
              is_root: false,
              visibility: 1,
            },
            {
              slice_id: publicSliceId,
              name: 'Public Demo',
              slug: `${username}/public-demo`,
              created_by: username,
              is_root: false,
              visibility: 2,
            },
          ],
        }),
      });
    });

    await page.goto('/login');
    await page.getByLabel('Username').fill(username);
    await page.getByRole('button', { name: /login with username/i }).click();

    await expect(page).toHaveURL(/\/slices(\?.*)?$/);
    await expect(page.locator('.slice-home-row-icon')).toHaveCount(0);
    await expect(page.locator('.slice-home-row-arrow')).toHaveCount(0);
    await expect(page.getByTestId('slice-home-list')).not.toContainText(/\d+\s+files/);

    const privateRow = page.getByTestId('slice-home-row').filter({ hasText: 'Private Demo' });
    await expect(privateRow.locator('.slice-home-chip--private')).toContainText('Private');

    const publicRow = page.getByTestId('slice-home-row').filter({ hasText: 'Public Demo' });
    await expect(publicRow.locator('.slice-home-chip--public')).toContainText('Public');

    const privateLayout = await privateRow.evaluate((row) => {
      const chip = row.querySelector('.slice-home-chip--visibility');
      const rowRect = row.getBoundingClientRect();
      const chipRect = chip.getBoundingClientRect();
      return {
        rowRight: Math.round(rowRect.right),
        chipRight: Math.round(chipRect.right),
      };
    });
    expect(privateLayout.rowRight - privateLayout.chipRight).toBeLessThanOrEqual(20);
  });
});

test.describe('Repo Browser Search', () => {
  test('submits indexed workspace search and renders structured results', async ({ page }) => {
    const username = `zzsrch${Date.now()}`;

    await page.goto('/login');
    await page.getByLabel('Username').fill(username);
    await page.getByRole('button', { name: /login with username/i }).click();

    await expect(page).toHaveURL(/\/slices(\?.*)?$/);
    await page.getByTestId('slice-home-row').first().click();
    await expect(page).toHaveURL(new RegExp(`/slices/home\\.${username}`));
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

    await expect(page).toHaveURL(/\/slices(\?.*)?$/);
    await page.getByTestId('slice-home-row').first().click();
    await expect(page).toHaveURL(new RegExp(`/slices/${sliceId.replace('.', '\\.')}`));

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
    await page.goto(`/slices/${sliceId}/commits`);

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
    const overflowChangesets = Array.from({ length: 12 }, (_, index) => ({
      changesetId: `cs-pending-overflow-${index}`,
      changesetHash: `hash-pending-overflow-${index}`,
      sliceId,
      baseCommitHash: `fs-base-${index + 3}`,
      modifiedFiles: [`${username}/overflow-${index}.md`],
      status: 'PENDING',
      author: username,
      createdAt: 1777955200 - index,
      message: `queued review ${index + 1} with enough title text to exercise row clipping and scrolling`,
    }));

    await page.setViewportSize({ width: 760, height: 900 });

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
      if (statusFilter === '3') {
        await new Promise((resolve) => setTimeout(resolve, 250));
      }
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
          message: 'draft todo update with a long reviewer note that should stay clipped inside the row',
        },
        ...overflowChangesets,
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
          message: 'merged notes update with release notes, generated docs, and review metadata',
        },
      ];
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          changesets: statusFilter === '3'
            ? allChangesets.filter((changeset) => changeset.status === 'MERGED')
            : allChangesets,
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
    await page.goto(`/slices/${sliceId}/changesets`);

    await expect(page.getByTestId('slice-changesets-page')).toBeVisible();
    await expect(page.getByTestId('slice-detail-tab-changesets')).toHaveAttribute('aria-selected', 'true');
    await expect(page.getByTestId('slice-changeset-row')).toHaveCount(14);
    expect(requests).toContain('all:true');
    const statusMetrics = await page.getByTestId('slice-changeset-row').evaluateAll((rows) => rows.map((row) => {
      const status = row.querySelector('.slice-activity-status');
      const rowRect = row.getBoundingClientRect();
      const statusRect = status.getBoundingClientRect();
      return {
        rightOffset: Math.round(rowRect.right - statusRect.right),
        width: Math.round(statusRect.width),
      };
    }));
    expect(new Set(statusMetrics.map((metric) => metric.width)).size).toBe(1);
    expect(new Set(statusMetrics.map((metric) => metric.rightOffset)).size).toBe(1);
    const listLayout = await page.getByTestId('slice-changesets-page').evaluate((pageElement) => {
      const panel = pageElement.querySelector('.slice-activity-panel');
      const row = pageElement.querySelector('[data-testid="slice-changeset-row"]');
      const panelStyle = window.getComputedStyle(panel);
      const rowStyle = window.getComputedStyle(row);
      const panelRect = panel.getBoundingClientRect();
      const rowRect = row.getBoundingClientRect();
      return {
        documentOverflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
        panelBorderTopWidth: Number.parseFloat(panelStyle.borderTopWidth),
        panelRadius: Number.parseFloat(panelStyle.borderTopLeftRadius),
        rowRadius: Number.parseFloat(rowStyle.borderTopLeftRadius),
        rowInsidePanel: rowRect.left >= panelRect.left - 1 && rowRect.right <= panelRect.right + 1,
      };
    });
    expect(listLayout.documentOverflow).toBeLessThanOrEqual(1);
    expect(listLayout.panelBorderTopWidth).toBeGreaterThan(0);
    expect(listLayout.panelRadius).toBeGreaterThan(0);
    expect(listLayout.rowRadius).toBe(0);
    expect(listLayout.rowInsidePanel).toBe(true);
    const rowTextLayout = await page.getByTestId('slice-changeset-row').first().evaluate((row) => {
      const title = row.querySelector('.slice-activity-row-title');
      const time = row.querySelector('.slice-activity-row-meta');
      const titleRect = title.getBoundingClientRect();
      const timeRect = time.getBoundingClientRect();
      return {
        titleAboveTime: titleRect.bottom <= timeRect.top + 1,
      };
    });
    expect(rowTextLayout.titleAboveTime).toBe(true);

    const bottomLayout = await page.getByTestId('slice-changesets-page').evaluate((pageElement) => {
      const content = pageElement.querySelector('.slice-activity-content');
      const rows = pageElement.querySelectorAll('[data-testid="slice-changeset-row"]');
      content.scrollTop = content.scrollHeight;
      const contentRect = content.getBoundingClientRect();
      const lastRowRect = rows[rows.length - 1].getBoundingClientRect();
      return {
        bottomGap: Math.round(contentRect.bottom - lastRowRect.bottom),
      };
    });
    expect(bottomLayout.bottomGap).toBeGreaterThan(8);

    await page.getByTestId('changeset-filter-merged').click();
    await expect(page.getByTestId('slice-changesets-summary')).not.toContainText('Loading changesets');
    await expect(page.getByTestId('slice-activity-loading')).toHaveCount(0);
    await expect(page.getByTestId('slice-changeset-row')).toHaveCount(14);
    await expect(page.getByTestId('slice-changeset-row')).toHaveCount(1);
    await expect(page.getByTestId('slice-changeset-row').first()).toContainText('merged notes update');
    const filteredStatusMetric = await page.getByTestId('slice-changeset-row').first().evaluate((row) => {
      const status = row.querySelector('.slice-activity-status');
      const rowRect = row.getBoundingClientRect();
      const statusRect = status.getBoundingClientRect();
      return {
        rightOffset: Math.round(rowRect.right - statusRect.right),
        width: Math.round(statusRect.width),
      };
    });
    expect(filteredStatusMetric).toEqual(statusMetrics[0]);
    expect(requests).toContain('3');

    await page.getByTestId('slice-changeset-row').first().click();
    await expect(page).toHaveURL(/\/changesets\/cs-merged-review/);
    await expect(page.getByTestId('changeset-diff-page')).toBeVisible();
  });

  test('keeps slice changeset rows readable at mobile width', async ({ page }) => {
    const username = `activitymobile${Date.now()}`;
    const sliceId = `home.${username}`;
    const changesets = Array.from({ length: 10 }, (_, index) => ({
      changesetId: `cs-mobile-review-${index}`,
      changesetHash: `hash-mobile-review-${index}`,
      sliceId,
      baseCommitHash: `fs-mobile-base-${index}`,
      modifiedFiles: [`${username}/notes/${index}/mobile-layout-review.md`],
      status: index % 3 === 0 ? 'MERGED' : 'PENDING',
      author: username,
      createdAt: 1777955400 - (index * 600),
      message: `mobile review ${index + 1} with a long changeset title that must not overlap the timestamp`,
    }));

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
              description: 'Mobile changeset layout test slice',
              owners: [username],
              created_by: username,
              is_root: false,
              file_count: 10,
            },
          ],
        }),
      });
    });

    await page.route(`**/v1/slices/${sliceId}/changesets**`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ changesets }),
      });
    });

    await page.goto('/login');
    await page.getByLabel('Username').fill(username);
    await page.getByRole('button', { name: /login with username/i }).click();
    await page.goto(`/slices/${sliceId}/changesets`);

    await expect(page.getByTestId('slice-changesets-page')).toBeVisible();
    await expect(page.getByTestId('slice-changeset-row')).toHaveCount(10);

    const mobileLayout = await page.getByTestId('slice-changesets-page').evaluate(async (pageElement) => {
      const content = pageElement.querySelector('.slice-activity-content');
      const rows = Array.from(pageElement.querySelectorAll('[data-testid="slice-changeset-row"]'));
      const firstRow = rows[0];
      const title = firstRow.querySelector('.slice-activity-row-title');
      const subtitle = firstRow.querySelector('.slice-activity-row-subtitle');
      const time = firstRow.querySelector('.slice-activity-row-meta');
      const status = firstRow.querySelector('.slice-activity-status');
      const arrow = firstRow.querySelector('.slice-activity-row-arrow');
      const filterButtons = Array.from(pageElement.querySelectorAll('.slice-activity-filter-btn'));
      const rowRect = firstRow.getBoundingClientRect();
      const titleRect = title.getBoundingClientRect();
      const subtitleRect = subtitle.getBoundingClientRect();
      const timeRect = time.getBoundingClientRect();
      const statusRect = status.getBoundingClientRect();
      const arrowRect = arrow.getBoundingClientRect();
      const filterButtonTop = filterButtons[0]?.getBoundingClientRect().top || 0;
      const filterSingleRow = filterButtons.every((button) => Math.abs(button.getBoundingClientRect().top - filterButtonTop) <= 4);
      const rectsOverlap = (a, b) => (
        a.left < b.right
        && a.right > b.left
        && a.top < b.bottom
        && a.bottom > b.top
      );

      content.scrollTop = content.scrollHeight;
      await new Promise(requestAnimationFrame);

      const contentRect = content.getBoundingClientRect();
      const lastRowRect = rows[rows.length - 1].getBoundingClientRect();
      return {
        documentOverflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
        contentOverflow: content.scrollWidth - content.clientWidth,
        firstRowOverflow: firstRow.scrollWidth - firstRow.clientWidth,
        firstRowVerticalOverflow: firstRow.scrollHeight - firstRow.clientHeight,
        filterSingleRow,
        titleTimeOverlap: rectsOverlap(titleRect, timeRect),
        titleInsideRow: titleRect.left >= rowRect.left - 1 && titleRect.right <= rowRect.right + 1,
        statusBelowTitle: statusRect.top >= titleRect.bottom - 1,
        subtitleBelowStatus: subtitleRect.top >= statusRect.bottom - 1,
        arrowInsideRow: arrowRect.left >= rowRect.left && arrowRect.right <= rowRect.right + 1,
        lastRowBottomGap: Math.round(contentRect.bottom - lastRowRect.bottom),
      };
    });

    expect(mobileLayout.documentOverflow).toBeLessThanOrEqual(1);
    expect(mobileLayout.contentOverflow).toBeLessThanOrEqual(1);
    expect(mobileLayout.firstRowOverflow).toBeLessThanOrEqual(1);
    expect(mobileLayout.firstRowVerticalOverflow).toBeLessThanOrEqual(1);
    expect(mobileLayout.filterSingleRow).toBe(true);
    expect(mobileLayout.titleTimeOverlap).toBe(false);
    expect(mobileLayout.titleInsideRow).toBe(true);
    expect(mobileLayout.statusBelowTitle).toBe(true);
    expect(mobileLayout.subtitleBelowStatus).toBe(true);
    expect(mobileLayout.arrowInsideRow).toBe(true);
    expect(mobileLayout.lastRowBottomGap).toBeGreaterThan(8);
  });

  test('keeps commit and changeset detail controls readable at mobile width', async ({ page }) => {
    const username = `detailmobile${Date.now()}`;
    const sliceId = `home.${username}`;
    const commitHash = 'fs-mobile-detail-commit-abcdef123456';
    const changesetId = 'cs-mobile-detail-review-abcdef123456';
    const patch = [
      '--- a/apps/mobile/detail-layout.jsx',
      '+++ b/apps/mobile/detail-layout.jsx',
      '@@ -1,3 +1,4 @@',
      ' import React from "react";',
      '-export const mode = "desktop";',
      '+export const mode = "mobile";',
      '+export const detail = "responsive";',
    ].join('\n');

    await page.setViewportSize({ width: 390, height: 844 });

    await page.route('**/v1/slices?limit=200', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          slices: [{
            slice_id: sliceId,
            name: sliceId,
            description: 'Mobile detail layout test slice',
            owners: [username],
            created_by: username,
            is_root: false,
            file_count: 4,
          }],
        }),
      });
    });

    await page.route(`**/v1/commits/${commitHash}/changes**`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          commit_hash: commitHash,
          files_added: 1,
          files_modified: 2,
          files_deleted: 1,
          files_renamed: 1,
          changes: [
            {
              id: 'commit-change-1',
              slice_id: sliceId,
              path: `${username}/apps/mobile/detail-layout.jsx`,
              change_type: 'modify',
              lines_added: 12,
              lines_deleted: 5,
              patch,
            },
            {
              id: 'commit-change-2',
              slice_id: sliceId,
              path: `${username}/docs/mobile/detail-layout-reference.md`,
              change_type: 'add',
              lines_added: 4,
              lines_deleted: 0,
              patch,
            },
          ],
        }),
      });
    });

    await page.route(`**/v1/changesets/${changesetId}/snapshots**`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          snapshots: [{
            snapshotId: 'snap-mobile-detail-1',
            changesetId,
            version: 2,
            author: username,
            createdAt: 1777955400,
            message: 'Mobile detail layout review snapshot',
          }],
        }),
      });
    });

    await page.route(`**/v1/changesets/${changesetId}/diff**`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          changeset: {
            changesetId,
            sliceId,
            status: 'PENDING',
            author: username,
            createdAt: 1777955400,
            message: 'Review mobile detail layout with long enough message to reveal cramped controls',
          },
          snapshot: {
            snapshotId: 'snap-mobile-detail-1',
            changesetId,
            version: 2,
            author: username,
            createdAt: 1777955400,
            message: 'Snapshot message for mobile detail view',
          },
          diff: {
            filesAdded: 1,
            filesModified: 2,
            filesDeleted: 1,
          },
          changes: [
            {
              id: 'changeset-change-1',
              path: `${username}/apps/mobile/detail-layout.jsx`,
              changeType: 'modify',
              linesAdded: 12,
              linesDeleted: 5,
              patch,
            },
            {
              id: 'changeset-change-2',
              path: `${username}/docs/mobile/detail-layout-reference.md`,
              changeType: 'add',
              linesAdded: 4,
              linesDeleted: 0,
              patch,
            },
          ],
        }),
      });
    });

    await page.goto('/login');
    await page.getByLabel('Username').fill(username);
    await page.getByRole('button', { name: /login with username/i }).click();

    await page.goto(`/diff/${commitHash}`);
    await expect(page.getByTestId('commit-diff-page')).toBeVisible();
    await expect(page.getByTestId('diff-file-item')).toHaveCount(2);

    const commitLayout = await page.getByTestId('commit-diff-page').evaluate((pageElement) => {
      const viewportWidth = document.documentElement.clientWidth;
      const topBar = pageElement.querySelector('.diff-top-bar');
      const controls = pageElement.querySelector('.diff-detail-controls');
      const filePanel = pageElement.querySelector('.diff-file-panel');
      const content = pageElement.querySelector('.diff-content');
      const firstFile = pageElement.querySelector('[data-testid="diff-file-item"]');
      const topBarRect = topBar.getBoundingClientRect();
      const controlsRect = controls.getBoundingClientRect();
      const contentRect = content.getBoundingClientRect();
      const firstFileRect = firstFile.getBoundingClientRect();
      return {
        documentOverflow: document.documentElement.scrollWidth - viewportWidth,
        filePanelHidden: getComputedStyle(filePanel).display === 'none',
        topBarHeight: Math.round(topBarRect.height),
        controlsInsideViewport: controlsRect.left >= 0 && controlsRect.right <= viewportWidth + 1,
        contentBelowHeader: contentRect.top >= topBarRect.bottom - 1,
        firstFileVisibleBelowHeader: firstFileRect.top >= contentRect.top - 1 && firstFileRect.top < contentRect.bottom,
      };
    });

    expect(commitLayout.documentOverflow).toBeLessThanOrEqual(1);
    expect(commitLayout.filePanelHidden).toBe(true);
    expect(commitLayout.topBarHeight).toBeLessThan(280);
    expect(commitLayout.controlsInsideViewport).toBe(true);
    expect(commitLayout.contentBelowHeader).toBe(true);
    expect(commitLayout.firstFileVisibleBelowHeader).toBe(true);

    const commitBottomLayout = await page.getByTestId('commit-diff-page').evaluate((pageElement) => {
      const content = pageElement.querySelector('.diff-content');
      const fileItems = pageElement.querySelectorAll('[data-testid="diff-file-item"]');
      const lastFile = fileItems[fileItems.length - 1];
      const footer = document.querySelector('.app-footer');
      content.scrollTop = content.scrollHeight;
      const contentRect = content.getBoundingClientRect();
      const lastFileRect = lastFile.getBoundingClientRect();
      const footerTop = footer?.getBoundingClientRect().top ?? window.innerHeight;
      return {
        scrolledToBottom: Math.abs(content.scrollTop + content.clientHeight - content.scrollHeight) <= 2,
        lastFileClearOfFooter: lastFileRect.bottom <= Math.min(contentRect.bottom, footerTop) - 1,
      };
    });

    expect(commitBottomLayout.scrolledToBottom).toBe(true);
    expect(commitBottomLayout.lastFileClearOfFooter).toBe(true);

    await page.goto(`/changesets/${changesetId}`);
    await expect(page.getByTestId('changeset-diff-page')).toBeVisible();
    await expect(page.getByTestId('changeset-file-item')).toHaveCount(2);

    const changesetLayout = await page.getByTestId('changeset-diff-page').evaluate((pageElement) => {
      const viewportWidth = document.documentElement.clientWidth;
      const topBar = pageElement.querySelector('.diff-top-bar');
      const controls = pageElement.querySelector('.changeset-detail-controls');
      const snapshotPicker = pageElement.querySelector('.changeset-snapshot-picker');
      const filePanel = pageElement.querySelector('.diff-file-panel');
      const content = pageElement.querySelector('.diff-content');
      const firstFile = pageElement.querySelector('[data-testid="changeset-file-item"]');
      const topBarRect = topBar.getBoundingClientRect();
      const controlsRect = controls.getBoundingClientRect();
      const pickerRect = snapshotPicker.getBoundingClientRect();
      const contentRect = content.getBoundingClientRect();
      const firstFileRect = firstFile.getBoundingClientRect();
      return {
        documentOverflow: document.documentElement.scrollWidth - viewportWidth,
        filePanelHidden: getComputedStyle(filePanel).display === 'none',
        topBarHeight: Math.round(topBarRect.height),
        controlsInsideViewport: controlsRect.left >= 0 && controlsRect.right <= viewportWidth + 1,
        pickerInsideViewport: pickerRect.left >= 0 && pickerRect.right <= viewportWidth + 1,
        contentBelowHeader: contentRect.top >= topBarRect.bottom - 1,
        firstFileVisibleBelowHeader: firstFileRect.top >= contentRect.top - 1 && firstFileRect.top < contentRect.bottom,
      };
    });

    expect(changesetLayout.documentOverflow).toBeLessThanOrEqual(1);
    expect(changesetLayout.filePanelHidden).toBe(true);
    expect(changesetLayout.topBarHeight).toBeLessThan(360);
    expect(changesetLayout.controlsInsideViewport).toBe(true);
    expect(changesetLayout.pickerInsideViewport).toBe(true);
    expect(changesetLayout.contentBelowHeader).toBe(true);
    expect(changesetLayout.firstFileVisibleBelowHeader).toBe(true);

    const changesetBottomLayout = await page.getByTestId('changeset-diff-page').evaluate((pageElement) => {
      const content = pageElement.querySelector('.diff-content');
      const fileItems = pageElement.querySelectorAll('[data-testid="changeset-file-item"]');
      const lastFile = fileItems[fileItems.length - 1];
      const footer = document.querySelector('.app-footer');
      content.scrollTop = content.scrollHeight;
      const contentRect = content.getBoundingClientRect();
      const lastFileRect = lastFile.getBoundingClientRect();
      const footerTop = footer?.getBoundingClientRect().top ?? window.innerHeight;
      return {
        scrolledToBottom: Math.abs(content.scrollTop + content.clientHeight - content.scrollHeight) <= 2,
        lastFileClearOfFooter: lastFileRect.bottom <= Math.min(contentRect.bottom, footerTop) - 1,
      };
    });

    expect(changesetBottomLayout.scrolledToBottom).toBe(true);
    expect(changesetBottomLayout.lastFileClearOfFooter).toBe(true);
  });
});

test.describe('Repo Browser Settings', () => {
  test('manages slice visibility without link panels', async ({ page }) => {
    const username = `webvisibility${Date.now()}`;
    const sliceId = `home.${username}`;
    const sliceName = `visibility-${username}`;
    const sliceSetBodies = [];
    let sliceVisibility = 1;

    await page.route('**/v1/slices?limit=200', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          slices: [
            {
              slice_id: sliceId,
              name: sliceName,
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

    await expect(page).toHaveURL(/\/slices(\?.*)?$/);
    await page.getByTestId('slice-home-row').first().click();
    await expect(page).toHaveURL(new RegExp(`/slices/${sliceId.replace('.', '\\.')}`));
    await expect(page.getByTestId('slice-detail-nav')).toContainText(sliceName);
    await expect(page.getByTestId('slice-detail-nav')).not.toContainText(sliceId);

    await page.getByTestId('repo-view-settings').click();
    await expect(page.getByTestId('slice-settings-panel')).toBeVisible();
    const settingsModalLayout = await page.locator('.slice-settings-modal').evaluate((modal) => {
      const header = modal.querySelector('.slice-settings-header');
      const close = modal.querySelector('.slice-settings-modal-close');
      const modalRect = modal.getBoundingClientRect();
      const closeRect = close.getBoundingClientRect();
      const headerStyle = window.getComputedStyle(header);
      return {
        width: Math.round(modalRect.width),
        closeLeft: Math.round(closeRect.left),
        closeRight: Math.round(closeRect.right),
        modalRight: Math.round(modalRect.right),
        headerPaddingRight: Math.round(parseFloat(headerStyle.paddingRight) || 0),
      };
    });
    expect(settingsModalLayout.width).toBeLessThanOrEqual(620);
    expect(settingsModalLayout.headerPaddingRight).toBeGreaterThanOrEqual(40);
    expect(settingsModalLayout.modalRight - settingsModalLayout.closeRight).toBeGreaterThanOrEqual(8);
    await expect(page.getByTestId('slice-visibility-status')).toContainText('Private');
    await expect(page.getByText('Git endpoint', { exact: true })).toHaveCount(0);
    await expect(page.getByText('Public slice URL', { exact: true })).toHaveCount(0);
    await expect(page.getByText('Raw file URL pattern', { exact: true })).toHaveCount(0);

    await page.getByTestId('slice-visibility-propagation').selectOption('public');
    await page.getByTestId('slice-visibility-set-public').click();
    await expect(page.getByTestId('slice-visibility-status')).toContainText('Public');
    expect(sliceSetBodies).toEqual([
      {
        visibility: 2,
        pathPropagationMode: 2,
      },
    ]);
  });
});
