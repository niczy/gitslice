import { test, expect } from '@playwright/test';

test('renders Git Slice landing content and navigation', async ({ page }) => {
  await page.goto('/');

  await expect(page.getByRole('heading', { level: 1, name: /check out a custom slice in seconds/i })).toBeVisible();
  await expect(page.getByRole('button', { name: /Git Slice/i })).toBeVisible();
  await expect(page.getByTestId('topbar-docs-link')).toBeVisible();
  await expect(page.getByTestId('topbar-github-link')).toBeVisible();
  await expect(page.getByTestId('topbar-get-started')).toBeVisible();
  await page.getByTestId('topbar-docs-link').click();
  await expect(page).toHaveURL(/\/docs$/);
  await expect(page.getByRole('heading', { level: 1, name: /one versioned filesystem, two work surfaces\./i })).toBeVisible();

  await page.goto('/slices');
  await expect(page.getByTestId('slice-home-page')).toBeVisible();

  await page.getByRole('button', { name: /Git Slice/i }).click();
  await expect(page.getByRole('heading', { level: 1, name: /check out a custom slice in seconds/i })).toBeVisible();
  await expect(page.getByRole('heading', { name: /make local work the main path when the task deserves a real checkout\./i })).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs slice create ui-refresh apps\/web/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs slice checkout <slice-id-or-slug>/i }).first()).toBeVisible();
  await expect(page.getByText(/gs slice diff/i)).toBeVisible();
  await expect(page.getByText(/gs slice publish --message "refresh settings page" --files src\/routes\/settings\.tsx/i)).toBeVisible();
  await expect(page.getByText(/gs fs write \/\$USER\/app\/NOTICE\.txt --text "hotfix shipped remotely"/i)).toBeVisible();
  await expect(page.getByText(/plain checkout now covers local status, diff, restore, sync, and publish on its own/i)).toBeVisible();
});

test('top navigation uses durable links and stays fixed while scrolling', async ({ page, browser, baseURL }) => {
  await page.goto('/');

  const brandIcon = page.locator('.brand-icon');
  const brandText = page.locator('.brand-text');
  await expect(brandIcon).toBeVisible();
  const homeBrandFontSize = await brandText.evaluate((element) => getComputedStyle(element).fontSize);

  const docsLink = page.getByTestId('topbar-docs-link');
  await expect(docsLink).toHaveAttribute('href', '/docs');
  const slicesLink = page.getByTestId('topbar-repo-browser');
  await expect(slicesLink).toHaveAttribute('href', '/slices');
  await expect(slicesLink).toContainText('Slices');
  await expect(page.getByTestId('topbar-get-started')).toHaveAttribute('href', '/');

  await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
  await expect(docsLink).toBeInViewport();
  await expect(page.locator('.top-bar')).toHaveCSS('position', 'fixed');

  await slicesLink.click();
  await expect(page).toHaveURL(/\/slices(\?.*)?$/);
  await expect(page.getByTestId('slice-home-page')).toBeVisible();
  await expect(page.locator('.app-shell')).not.toHaveClass(/app-shell--browser/);
  await expect(brandIcon).toBeVisible();
  await expect(brandText).toHaveCSS('font-size', homeBrandFontSize);
  const slicesHeaderHeight = await page.locator('.top-bar').evaluate((element) => element.getBoundingClientRect().height);
  const slicesBrandFontSize = await brandText.evaluate((element) => getComputedStyle(element).fontSize);
  const slicesBrandIconBox = await brandIcon.evaluate((element) => {
    const rect = element.getBoundingClientRect();
    return { width: rect.width, height: rect.height };
  });
  const slicesBrandIconLeft = await brandIcon.evaluate((element) => element.getBoundingClientRect().left);
  const slicesNavButtonHeight = await slicesLink.evaluate((element) => element.getBoundingClientRect().height);

  await page.goto('/slices/root_slice');
  await expect(page.getByTestId('slice-detail-nav')).toBeVisible();
  const sliceDetailHeaderHeight = await page.locator('.top-bar').evaluate((element) => element.getBoundingClientRect().height);
  expect(sliceDetailHeaderHeight).toBe(slicesHeaderHeight);
  await expect(brandText).toHaveCSS('font-size', slicesBrandFontSize);
  await expect(brandIcon).toHaveJSProperty('offsetWidth', slicesBrandIconBox.width);
  await expect(brandIcon).toHaveJSProperty('offsetHeight', slicesBrandIconBox.height);
  const sliceDetailBrandIconLeft = await brandIcon.evaluate((element) => element.getBoundingClientRect().left);
  const sliceDetailTitleLeft = await page.locator('.slice-detail-nav-title').evaluate((element) => element.getBoundingClientRect().left);
  const fileTreeTitleLeft = await page.getByRole('heading', { name: /File tree/i }).evaluate((element) => element.getBoundingClientRect().left);
  expect(sliceDetailBrandIconLeft).toBe(slicesBrandIconLeft);
  expect(sliceDetailBrandIconLeft).toBe(sliceDetailTitleLeft);
  expect(fileTreeTitleLeft).toBe(sliceDetailTitleLeft);
  const sliceDetailNavButtonHeight = await page.getByTestId('topbar-repo-browser').evaluate((element) => element.getBoundingClientRect().height);
  expect(sliceDetailNavButtonHeight).toBe(slicesNavButtonHeight);

  const noJsContext = await browser.newContext({ baseURL, javaScriptEnabled: false });
  const noJsPage = await noJsContext.newPage();
  await noJsPage.goto('/');
  await noJsPage.getByTestId('topbar-docs-link').click();
  await expect(noJsPage).toHaveURL(/\/docs$/);
  await noJsContext.close();
});
