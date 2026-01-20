import { test, expect } from '@playwright/test';

test('lists and creates slices from the web console', async ({ page }) => {
  const slices = [
    {
      slice_id: 'root_slice',
      name: 'Root Slice',
      description: 'Base snapshot',
      owners: ['platform'],
      created_by: 'system',
      created_at: '2024-01-10T12:00:00Z',
      updated_at: '2024-01-11T08:30:00Z',
      file_count: 12,
      is_root: true,
    },
  ];

  await page.route('**/v1/slices?limit=100', async (route, request) => {
    if (request.method() !== 'GET') {
      await route.fallback();
      return;
    }

    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ slices }),
    });
  });

  await page.route('**/v1/slices', async (route, request) => {
    if (request.method() !== 'POST') {
      await route.fallback();
      return;
    }

    const payload = JSON.parse(request.postData() || '{}');
    slices.push({
      slice_id: payload.slice_id,
      name: payload.name,
      description: payload.description,
      owners: payload.owners,
      created_by: payload.created_by,
      created_at: '2024-02-12T09:00:00Z',
      updated_at: '2024-02-12T09:00:00Z',
      file_count: payload.files?.length || 0,
      is_root: false,
    });

    await route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({ slice_id: payload.slice_id, status: 'created' }),
    });
  });

  await page.goto('/');
  await page.getByRole('button', { name: 'Slices' }).click();

  await expect(page.getByRole('heading', { name: /Manage existing slices/i })).toBeVisible();
  await expect(page.getByText('Root Slice')).toBeVisible();
  await expect(page.getByText('root_slice')).toBeVisible();

  await page.getByLabel('Slice ID').fill('feature_payments');
  await page.getByLabel('Name').fill('Payments flow');
  await page.getByLabel('Description').fill('Add payment integration');
  await page.getByLabel('Owners').fill('alice, bob');
  await page.getByLabel('Files').fill('src/payments.js, src/billing.js');
  await page.getByLabel('Created by').fill('alice');
  await page.getByRole('button', { name: /Create slice/i }).click();

  await expect(page.getByText(/Slice created successfully/i)).toBeVisible();
  await expect(page.getByText('Payments flow')).toBeVisible();
  await expect(page.getByText('feature_payments')).toBeVisible();
});
