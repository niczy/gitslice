import { test, expect } from '@playwright/test';

test.describe('Slice creation', () => {
  test('creates a slice with selected tracked folders', async ({ page }) => {
    const username = 'sliceweb1';
    let createPayload = null;

    await page.route('**/v1/slices?limit=200', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          slices: [
            {
              slice_id: `home.${username}`,
              name: `${username} home`,
              slug: username,
              description: 'Home slice',
              owners: [username],
              created_by: username,
              is_root: false,
              file_count: 2,
            },
            {
              slice_id: 'root_slice',
              name: 'Root Slice',
              slug: 'root',
              description: 'Root slice',
              owners: ['system'],
              created_by: 'system',
              is_root: true,
              file_count: 2,
            },
          ],
        }),
      });
    });

    await page.route('**/v1/slices/root_slice/entries**', async (route) => {
      const url = new URL(route.request().url());
      const path = decodeURIComponent(url.pathname.replace('/v1/slices/root_slice/entries', '').replace(/^\/+/, ''));
      const entries = path
        ? []
        : [
            {
              id: 'root_slice:web',
              name: 'web',
              path: 'web',
              type: 'ENTRY_TYPE_DIRECTORY',
            },
            {
              id: 'root_slice:services',
              name: 'services',
              path: 'services',
              type: 'ENTRY_TYPE_DIRECTORY',
            },
          ];
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ entries }),
      });
    });

    await page.route('**/v1/slices:createFromFolder', async (route) => {
      createPayload = route.request().postDataJSON();
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          sliceId: 'slice-web',
          status: 'created',
          name: 'Web work',
          slug: `${username}/web-work`,
          files: ['web/src/App.jsx'],
        }),
      });
    });

    await page.goto('/login');
    await page.getByLabel('Username').fill(username);
    await page.getByRole('button', { name: /login with username/i }).click();
    await expect(page).toHaveURL(/\/browser(\?.*)?$/);

    await expect(page.getByTestId('slice-create-open')).not.toHaveCSS('color', 'rgb(255, 255, 255)');
    await page.getByTestId('slice-create-open').click();
    await expect(page.getByRole('dialog', { name: /create slice/i })).toBeVisible();
    await expect(page.getByTestId('slice-create-submit')).not.toHaveCSS('color', 'rgb(255, 255, 255)');
    await page.getByTestId('slice-create-name').fill('Web work');
    await page.getByTestId('slice-create-folder-option').filter({ hasText: /^web/ }).first().click();
    await expect(page.getByTestId('slice-create-selected-folders')).toContainText('web');
    await page.getByTestId('slice-create-folder-input').fill('services/slice');
    await page.getByRole('button', { name: /^add$/i }).click();

    await page.getByTestId('slice-create-submit').click();

    await expect.poll(() => createPayload).not.toBeNull();
    expect(createPayload).toMatchObject({
      parentSliceId: 'root_slice',
      folderPaths: ['web', 'services/slice'],
      name: 'Web work',
    });
    expect(createPayload).not.toHaveProperty('folderPath');
    await expect(page).toHaveURL(/\/browser\/slice-web/);
  });
});
