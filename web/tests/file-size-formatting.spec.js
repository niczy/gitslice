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
  await expect(page.getByRole('button', { name: /small\.txt/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /medium\.txt/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /large\.txt/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /folder/ })).toBeVisible();

  // Verify file sizes are formatted correctly by checking the button content
  const smallBtn = page.getByRole('button', { name: /small\.txt/ });
  await expect(smallBtn).toContainText('0 B');

  const mediumBtn = page.getByRole('button', { name: /medium\.txt/ });
  await expect(mediumBtn).toContainText('1.0 KB'); // 1024 bytes shows as 1.0 KB (1 decimal for < 10)

  const largeBtn = page.getByRole('button', { name: /large\.txt/ });
  await expect(largeBtn).toContainText('1.0 MB'); // 1048576 bytes shows as 1.0 MB
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

  // Verify all files are visible (use exact match to avoid ambiguity)
  await expect(page.getByRole('button', { name: /^.*zero\.txt.*$/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /^.*📄.*bytes\.txt / })).toBeVisible(); // Space after to avoid matching kilobytes
  await expect(page.getByRole('button', { name: /kilobytes\.txt/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /megabytes\.txt/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /gigabytes\.txt/ })).toBeVisible();

  // Verify size formatting
  await expect(page.getByRole('button', { name: /zero\.txt/ })).toContainText('0 B');
  await expect(page.getByRole('button', { name: /^.*📄.*bytes\.txt / })).toContainText('500 B');
  await expect(page.getByRole('button', { name: /kilobytes\.txt/ })).toContainText('2.0 KB'); // 2048 bytes shows as 2.0 KB
  await expect(page.getByRole('button', { name: /megabytes\.txt/ })).toContainText('5.0 MB'); // Shows 1 decimal for < 10
  await expect(page.getByRole('button', { name: /gigabytes\.txt/ })).toContainText('2.0 GB'); // Shows 1 decimal for < 10
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
  await expect(page.getByRole('button', { name: /README\.md/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /📁.*src/ })).toBeVisible(); // Directory has folder emoji
  await expect(page.getByRole('button', { name: /package\.json/ })).toBeVisible();

  // Verify file sizes in the buttons
  await expect(page.getByRole('button', { name: /README\.md/ })).toContainText('1.2 KB');
  await expect(page.getByRole('button', { name: /package\.json/ })).toContainText('523 B');

  // Click to expand the src folder
  await page.getByRole('button', { name: /📁.*src/ }).click();

  // Verify the nested file is visible
  await expect(page.getByRole('button', { name: /index\.js/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /index\.js/ })).toContainText('4.5 KB');

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
  await expect(page.getByRole('button', { name: /empty\.txt/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /null\.txt/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /valid\.txt/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /1023\.txt/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /1024\.txt/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /1025\.txt/ })).toBeVisible();

  // Verify no JavaScript errors
  expect(errors).toHaveLength(0);

  // Verify size formatting for edge cases
  // Empty and null should show as "0 B"
  await expect(page.getByRole('button', { name: /empty\.txt/ })).toContainText('0 B');
  await expect(page.getByRole('button', { name: /null\.txt/ })).toContainText('0 B');

  // Valid small file
  await expect(page.getByRole('button', { name: /valid\.txt/ })).toContainText('100 B');

  // 1023 bytes stays in bytes
  await expect(page.getByRole('button', { name: /1023\.txt/ })).toContainText('1023 B');

  // 1024 bytes becomes 1.0 KB (shows 1 decimal for values < 10 in the unit)
  await expect(page.getByRole('button', { name: /1024\.txt/ })).toContainText('1.0 KB');

  // 1025 bytes becomes 1.0 KB (shows decimal for < 10 KB)
  await expect(page.getByRole('button', { name: /1025\.txt/ })).toContainText('1.0 KB');
});
