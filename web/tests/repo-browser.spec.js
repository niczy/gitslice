import { test, expect } from '@playwright/test';

test('browses the repo tree and previews a file', async ({ page }) => {
  await page.route('**/v1/slices/root_slice/entries**', async (route, request) => {
    const url = new URL(request.url());
    const path = url.searchParams.get('path') || '';

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

  await page.route('**/v1/slices/root_slice/files**', async (route, request) => {
    const url = new URL(request.url());
    const path = url.searchParams.get('path');
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
  await page.getByRole('button', { name: 'Repo Browser' }).click();

  await expect(page.getByRole('heading', { name: /File tree/i })).toBeVisible();

  await page.getByRole('button', { name: /apps/i }).click();
  await expect(page.getByRole('button', { name: /readme\.md/i })).toBeVisible();

  await page.getByRole('button', { name: /readme\.md/i }).click();
  await expect(page.getByRole('heading', { name: /apps\/readme\.md/i })).toBeVisible();
  await expect(page.getByText('# Hello')).toBeVisible();
});
