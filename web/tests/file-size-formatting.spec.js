import { test, expect } from '@playwright/test';

test('handles string size values from API without errors', async ({ page }) => {
  // Mock the API to return size as strings (the bug scenario)
  await page.route('**/v1/slices/root_slice/entries**', async (route, request) => {
    const url = new URL(request.url());
    const path = url.searchParams.get('path') || '';

    if (path === '') {
      // Genesis directory - return sizes as strings to test the bug fix
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            { name: 'small.txt', path: 'small.txt', type: 'ENTRY_TYPE_FILE', size: '0', has_children: false },
            { name: 'medium.txt', path: 'medium.txt', type: 'ENTRY_TYPE_FILE', size: '1024', has_children: false },
            { name: 'large.txt', path: 'large.txt', type: 'ENTRY_TYPE_FILE', size: '1048576', has_children: false },
            { name: 'folder', path: 'folder', type: 'ENTRY_TYPE_DIRECTORY', size: '0', has_children: true },
          ],
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

  // Navigate to repo browser
  await page.goto('/');
  await page.getByRole('button', { name: 'Repo Browser' }).click();

  // Verify the page loads without JavaScript errors
  await expect(page.getByRole('heading', { name: /File tree/i })).toBeVisible();

  // Verify all file entries are visible (no crash from toFixed error)
  await expect(page.getByText('small.txt')).toBeVisible();
  await expect(page.getByText('medium.txt')).toBeVisible();
  await expect(page.getByText('large.txt')).toBeVisible();
  await expect(page.getByText('folder')).toBeVisible();

  // Verify file sizes are formatted correctly
  await expect(page.getByText('0 B')).toBeVisible(); // small.txt
  await expect(page.getByText('1 KB')).toBeVisible(); // medium.txt (1024 bytes)
  await expect(page.getByText('1 MB')).toBeVisible(); // large.txt (1048576 bytes)
});

test('formats numeric size values correctly', async ({ page }) => {
  // Mock the API to return size as numbers (correct type)
  await page.route('**/v1/slices/root_slice/entries**', async (route, request) => {
    const url = new URL(request.url());
    const path = url.searchParams.get('path') || '';

    if (path === '') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            { name: 'zero.txt', path: 'zero.txt', type: 'ENTRY_TYPE_FILE', size: 0, has_children: false },
            { name: 'bytes.txt', path: 'bytes.txt', type: 'ENTRY_TYPE_FILE', size: 500, has_children: false },
            { name: 'kilobytes.txt', path: 'kilobytes.txt', type: 'ENTRY_TYPE_FILE', size: 2048, has_children: false },
            { name: 'megabytes.txt', path: 'megabytes.txt', type: 'ENTRY_TYPE_FILE', size: 5242880, has_children: false },
            { name: 'gigabytes.txt', path: 'gigabytes.txt', type: 'ENTRY_TYPE_FILE', size: 2147483648, has_children: false },
          ],
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

  await page.goto('/');
  await page.getByRole('button', { name: 'Repo Browser' }).click();

  await expect(page.getByRole('heading', { name: /File tree/i })).toBeVisible();

  // Verify all files are visible
  await expect(page.getByText('zero.txt')).toBeVisible();
  await expect(page.getByText('bytes.txt')).toBeVisible();
  await expect(page.getByText('kilobytes.txt')).toBeVisible();
  await expect(page.getByText('megabytes.txt')).toBeVisible();
  await expect(page.getByText('gigabytes.txt')).toBeVisible();

  // Verify size formatting
  await expect(page.getByText('0 B')).toBeVisible(); // 0 bytes
  await expect(page.getByText('500 B')).toBeVisible(); // 500 bytes
  await expect(page.getByText('2 KB')).toBeVisible(); // 2048 bytes
  await expect(page.getByText('5 MB')).toBeVisible(); // 5242880 bytes
  await expect(page.getByText('2 GB')).toBeVisible(); // 2147483648 bytes
});

test('clicks genesis directory and expands folders without errors', async ({ page }) => {
  // Test the specific scenario mentioned in the bug report
  await page.route('**/v1/slices/root_slice/entries**', async (route, request) => {
    const url = new URL(request.url());
    const path = url.searchParams.get('path') || '';

    if (path === '') {
      // Genesis directory with mixed string/number sizes
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            { name: 'README.md', path: 'README.md', type: 'ENTRY_TYPE_FILE', size: '1234', has_children: false },
            { name: 'src', path: 'src', type: 'ENTRY_TYPE_DIRECTORY', size: '0', has_children: true },
            { name: 'package.json', path: 'package.json', type: 'ENTRY_TYPE_FILE', size: 523, has_children: false },
          ],
        }),
      });
      return;
    }

    if (path === 'src') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            { name: 'index.js', path: 'src/index.js', type: 'ENTRY_TYPE_FILE', size: '4567', has_children: false },
          ],
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

  // Monitor for JavaScript errors
  const errors = [];
  page.on('pageerror', (error) => {
    errors.push(error.message);
  });

  await page.goto('/');
  await page.getByRole('button', { name: 'Repo Browser' }).click();

  // Click on the genesis directory root (expand it)
  await expect(page.getByRole('heading', { name: /File tree/i })).toBeVisible();

  // Verify no JavaScript errors occurred
  expect(errors).toHaveLength(0);

  // Verify files are displayed with sizes
  await expect(page.getByText('README.md')).toBeVisible();
  await expect(page.getByText('src')).toBeVisible();
  await expect(page.getByText('package.json')).toBeVisible();

  // Click to expand the src folder
  await page.getByRole('button', { name: /src/i }).click();

  // Verify the nested file is visible
  await expect(page.getByText('index.js')).toBeVisible();

  // Verify still no JavaScript errors
  expect(errors).toHaveLength(0);
});

test('handles edge cases in file size formatting', async ({ page }) => {
  await page.route('**/v1/slices/root_slice/entries**', async (route, request) => {
    const url = new URL(request.url());
    const path = url.searchParams.get('path') || '';

    if (path === '') {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          entries: [
            // Edge cases: null, undefined, empty string, invalid values
            { name: 'empty.txt', path: 'empty.txt', type: 'ENTRY_TYPE_FILE', size: '', has_children: false },
            { name: 'null.txt', path: 'null.txt', type: 'ENTRY_TYPE_FILE', size: null, has_children: false },
            { name: 'valid.txt', path: 'valid.txt', type: 'ENTRY_TYPE_FILE', size: 100, has_children: false },
            { name: '1023.txt', path: '1023.txt', type: 'ENTRY_TYPE_FILE', size: 1023, has_children: false },
            { name: '1024.txt', path: '1024.txt', type: 'ENTRY_TYPE_FILE', size: 1024, has_children: false },
            { name: '1025.txt', path: '1025.txt', type: 'ENTRY_TYPE_FILE', size: 1025, has_children: false },
          ],
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

  const errors = [];
  page.on('pageerror', (error) => {
    errors.push(error.message);
  });

  await page.goto('/');
  await page.getByRole('button', { name: 'Repo Browser' }).click();

  await expect(page.getByRole('heading', { name: /File tree/i })).toBeVisible();

  // Verify all files are displayed without errors
  await expect(page.getByText('empty.txt')).toBeVisible();
  await expect(page.getByText('null.txt')).toBeVisible();
  await expect(page.getByText('valid.txt')).toBeVisible();
  await expect(page.getByText('1023.txt')).toBeVisible();
  await expect(page.getByText('1024.txt')).toBeVisible();
  await expect(page.getByText('1025.txt')).toBeVisible();

  // Verify no JavaScript errors
  expect(errors).toHaveLength(0);

  // Verify size formatting for edge cases
  // Empty and null should show as "0 B"
  const zeroBElements = await page.getByText('0 B').all();
  expect(zeroBElements.length).toBeGreaterThanOrEqual(2); // empty.txt and null.txt

  // 1023 bytes stays in bytes
  await expect(page.getByText('1023 B')).toBeVisible();

  // 1024 bytes becomes 1 KB
  await expect(page.getByText('1 KB')).toBeVisible();

  // 1025 bytes becomes 1.0 KB (shows decimal for < 10 KB)
  await expect(page.getByText(/1\.0 KB/)).toBeVisible();
});
