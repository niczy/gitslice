import { test, expect } from '@playwright/test';

// Genesis populates files under o/genesis/projects/gitslice/
// so root entries should include the "o" directory.

async function ensureRootRepositoryVisible(page) {
  await page.getByTestId('slice-dropdown-trigger').click();
  await expect(page.getByTestId('slice-list')).toBeVisible();

  const rootSliceItem = page
    .getByTestId('slice-dropdown-item')
    .filter({ hasText: /root_slice|root slice|\broot\b/i })
    .first();

  if (await rootSliceItem.count()) {
    await rootSliceItem.click();
  } else {
    await page.keyboard.press('Escape');
  }
}

test.describe('Root Repository Browsing (real server)', () => {
  test('loads root entries and shows genesis directory tree', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    await ensureRootRepositoryVisible(page);

    // The navigator should render the full folder path, even at the root level.
    await expect(page.getByRole('button', { name: /📁.*\/o$/i })).toBeVisible();
  });

  test('navigates into genesis directory and finds repo files', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    await ensureRootRepositoryVisible(page);

    // Navigate: o -> genesis -> projects -> gitslice
    await page.getByRole('button', { name: /📁.*\/o$/i }).click();
    await expect(page.getByRole('button', { name: /📁.*genesis/i })).toBeVisible();

    await page.getByRole('button', { name: /📁.*genesis/i }).click();
    await expect(page.getByRole('button', { name: /📁.*projects/i })).toBeVisible();

    await page.getByRole('button', { name: /📁.*projects/i }).click();
    await expect(page.getByRole('button', { name: /📁.*gitslice/i })).toBeVisible();

    await page.getByRole('button', { name: /📁.*gitslice/i }).click();

    // Should see real repo files
    await expect(page.getByRole('button', { name: /README\.md/i })).toBeVisible();
    await expect(page.getByRole('button', { name: /go\.mod/i })).toBeVisible();
    await expect(page.getByRole('button', { name: /Makefile/i })).toBeVisible();
  });

  test('previews a real file from the genesis repository', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    await ensureRootRepositoryVisible(page);

    // Navigate to gitslice root (wait for each level to load)
    await page.getByRole('button', { name: /📁.*\/o$/i }).click();
    await expect(page.getByRole('button', { name: /📁.*genesis/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*genesis/i }).click();
    await expect(page.getByRole('button', { name: /📁.*projects/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*projects/i }).click();
    await expect(page.getByRole('button', { name: /📁.*gitslice/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*gitslice/i }).click();

    // Click README.md to preview it
    await page.getByRole('button', { name: /README\.md/i }).click();

    // The code header breadcrumbs should include the selected file.
    await expect(page.locator('.code-header .breadcrumb').filter({ hasText: /README\.md/i })).toBeVisible();

    // The real README.md should contain some content (it's a gitslice project)
    // Check for common markdown content that should be in the file
    const preview = page.locator('.file-preview');
    await expect(preview).toBeVisible();
    await expect(preview).not.toBeEmpty();
  });

  test('navigates into subdirectories and back', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    await ensureRootRepositoryVisible(page);

    // Navigate to gitslice root (wait for each level to load)
    await page.getByRole('button', { name: /📁.*\/o$/i }).click();
    await expect(page.getByRole('button', { name: /📁.*genesis/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*genesis/i }).click();
    await expect(page.getByRole('button', { name: /📁.*projects/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*projects/i }).click();
    await expect(page.getByRole('button', { name: /📁.*gitslice/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*gitslice/i }).click();

    // Navigate into internal/ subdirectory
    await page.getByRole('button', { name: /📁.*internal/i }).click();

    // Should see child entries under internal (type classification varies between runs)
    await expect(page.getByRole('button', { name: /common|config|models|services|storage/i }).first()).toBeVisible();
  });


  test('keeps selected file open after refresh', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    await ensureRootRepositoryVisible(page);

    await page.getByRole('button', { name: /📁.*\/o$/i }).click();
    await expect(page.getByRole('button', { name: /📁.*genesis/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*genesis/i }).click();
    await expect(page.getByRole('button', { name: /📁.*projects/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*projects/i }).click();
    await expect(page.getByRole('button', { name: /📁.*gitslice/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*gitslice/i }).click();

    await page.getByRole('button', { name: /README\.md/i }).click();
    await expect(page.locator('.code-header .breadcrumb').filter({ hasText: /README\.md/i })).toBeVisible();

    await page.reload();

    await page.getByTestId('topbar-repo-browser').click();
    await expect(page.locator('.code-header .breadcrumb').filter({ hasText: /README\.md/i })).toBeVisible();
  });
  test('shows file sizes in the entry list', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    await ensureRootRepositoryVisible(page);

    // Navigate to gitslice root (wait for each level to load)
    await page.getByRole('button', { name: /📁.*\/o$/i }).click();
    await expect(page.getByRole('button', { name: /📁.*genesis/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*genesis/i }).click();
    await expect(page.getByRole('button', { name: /📁.*projects/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*projects/i }).click();
    await expect(page.getByRole('button', { name: /📁.*gitslice/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*gitslice/i }).click();

    // README.md should have a file size displayed (any valid size format)
    const readmeBtn = page.getByRole('button', { name: /README\.md/i });
    await expect(readmeBtn).toBeVisible();
    // Size should contain B, KB, or MB
    await expect(readmeBtn).toContainText(/\d+(\.\d+)?\s*(B|KB|MB)/);
  });
});

test.describe('Slice-specific Browsing (real server)', () => {
  test('browses root_slice in slice mode', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    await ensureRootRepositoryVisible(page);

    await expect(page.getByRole('heading', { name: /File tree/i })).toBeVisible();

    // Should see the "o" directory (genesis files)
    await expect(page.getByRole('button', { name: /📁.*\/o$/i })).toBeVisible();
  });
});

test.describe('Repo Browser Search', () => {
  test('submits indexed workspace search and renders structured results', async ({ page }) => {
    const username = `browsersearch${Date.now()}`;

    await page.goto('/login');
    await page.getByLabel('Username').fill(username);
    await page.getByRole('button', { name: /login with username/i }).click();

    await expect(page).toHaveURL(/\/browser(\?.*)?$/);
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

test.describe('Repo Browser Settings', () => {
  test('manages slice and folder visibility with public links', async ({ page }) => {
    const username = `webvisibility${Date.now()}`;
    const sliceId = `home.${username}`;
    const sliceSetBodies = [];
    const pathSetBodies = [];
    let sliceVisibility = 1;
    let pathExplicitRule = false;
    let pathRuleVisibility = 1;
    let pathResolvedFromPath = '';

    const effectivePathVisibility = () => {
      if (sliceVisibility === 2) {
        return 2;
      }
      return pathExplicitRule ? pathRuleVisibility : 1;
    };

    const pathVisibilityPayload = (path) => ({
      workspace_id: sliceId,
      visibility: {
        path,
        visibility: pathExplicitRule ? pathRuleVisibility : 1,
        explicit_rule: pathExplicitRule,
        resolved_from_path: pathResolvedFromPath,
        effective_visibility: effectivePathVisibility(),
      },
    });

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

    await page.route(`**/v1/slices/${sliceId}/entries**`, async (route) => {
      const requestUrl = new URL(route.request().url());
      const decodedPath = decodeURIComponent(requestUrl.pathname.split('/entries')[1] || '').replace(/^\/+/, '');
      const entries = decodedPath === 'docs'
        ? [
            {
              id: `${sliceId}:docs/guide.md`,
              name: 'guide.md',
              path: 'docs/guide.md',
              type: 'ENTRY_TYPE_FILE',
              size: 18,
            },
          ]
        : [
            {
              id: `${sliceId}:docs`,
              name: 'docs',
              path: 'docs',
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

    await page.route('**/v1/fs:visibility?*', async (route) => {
      const requestUrl = new URL(route.request().url());
      expect(requestUrl.searchParams.get('workspace_id')).toBe(sliceId);
      expect(requestUrl.searchParams.get('path')).toBe('/docs');
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify(pathVisibilityPayload('/docs')),
      });
    });

    await page.route('**/v1/fs:visibility', async (route) => {
      const payload = JSON.parse(route.request().postData() || '{}');
      pathSetBodies.push(payload);
      pathExplicitRule = true;
      pathRuleVisibility = payload.visibility;
      pathResolvedFromPath = payload.path;
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          recursive: Boolean(payload.recursive),
          visibility: {
            path: payload.path,
            visibility: pathRuleVisibility,
            explicit_rule: true,
            resolved_from_path: pathResolvedFromPath,
            effective_visibility: effectivePathVisibility(),
          },
        }),
      });
    });

    await page.goto('/login');
    await page.getByLabel('Username').fill(username);
    await page.getByRole('button', { name: /login with username/i }).click();

    await expect(page).toHaveURL(/\/browser(\?.*)?$/);
    await expect(page.getByRole('button', { name: /📁.*\/docs/i })).toBeVisible();

    await page.getByRole('button', { name: /📁.*\/docs/i }).click();
    await expect(page.getByRole('button', { name: /guide\.md/i })).toBeVisible();

    await page.getByTestId('repo-view-settings').click();
    await expect(page.getByTestId('slice-settings-panel')).toBeVisible();
    await expect(page.getByTestId('slice-visibility-status')).toContainText('Private');
    await expect(page.getByTestId('path-visibility-status')).toContainText('Private');
    await expect(page.getByTestId('path-visibility-panel')).toContainText('/docs');

    await page.getByTestId('path-visibility-set-public').click();
    await expect(page.getByTestId('path-visibility-status')).toContainText('Public');
    await expect(page.getByTestId('path-visibility-panel')).toContainText('Explicit rule');
    expect(pathSetBodies).toEqual([
      {
        path: '/docs',
        visibility: 2,
        recursive: true,
      },
    ]);

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
    const pathUrl = `${origin}/v1/public/entries/docs?slice_id=${sliceId}`;

    await expect(page.getByTestId('slice-visibility-url')).toHaveValue(sliceUrl);
    await expect(page.getByTestId('path-visibility-url')).toHaveValue(pathUrl);

    await page.getByTestId('slice-visibility-copy-url').click();
    await page.getByTestId('path-visibility-copy-url').click();

    await expect(page.getByTestId('slice-visibility-copy-url')).toContainText('Copy public URL');
    const copiedTexts = await page.evaluate(() => window.__copiedTexts);
    expect(copiedTexts).toEqual([sliceUrl, pathUrl]);
  });
});
