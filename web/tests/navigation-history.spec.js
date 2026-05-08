// @ts-check
import { test, expect } from '@playwright/test';

test.describe('Navigation history and URL reloading', () => {
  const landingTitle = /check out a custom slice in seconds/i;

  const waitForLanding = async (page) => {
    const landingHeading = page.getByRole('heading', { level: 1, name: landingTitle });
    for (let i = 0; i < 6; i += 1) {
      if (await landingHeading.isVisible()) {
        return;
      }
      await page.goBack();
      await page.waitForTimeout(200);
    }
    await expect(landingHeading).toBeVisible();
  };

  const waitForBrowser = async (page) => {
    const browserTrigger = page.getByTestId('slice-detail-nav');
    for (let i = 0; i < 6; i += 1) {
      if (await browserTrigger.isVisible()) {
        return;
      }
      await page.goForward();
      await page.waitForTimeout(200);
    }
    await expect(browserTrigger).toBeVisible();
  };

  const escapeRegExp = (value) => String(value).replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

  const openFolderPreviewEntry = async (page, name) => {
    const entry = page.locator('.folder-preview-entry').filter({
      has: page.locator('.folder-preview-entry-name').filter({ hasText: new RegExp(`^${escapeRegExp(name)}$`, 'i') }),
    }).first();
    await expect(entry).toBeVisible();
    await entry.click();
  };

  const openFilePreviewEntry = async (page, name) => {
    const entry = page.locator('.folder-preview-entry').filter({
      has: page.locator('.folder-preview-entry-name').filter({ hasText: new RegExp(`^${escapeRegExp(name)}$`, 'i') }),
    }).first();
    await expect(entry).toBeVisible();
    await entry.click();
  };

  const openGenesisReadme = async (page) => {
    await openFolderPreviewEntry(page, 'o');
    await openFolderPreviewEntry(page, 'genesis');
    await openFolderPreviewEntry(page, 'projects');
    await openFolderPreviewEntry(page, 'gitslice');

    await openFilePreviewEntry(page, 'README.md');
    await expect(page.locator('.file-preview')).toBeVisible();
  };

  test('navigating to repo browser updates the URL path', async ({ page }) => {
    await page.goto('/');
    await page.goto('/slices/root');

    await expect(page.getByTestId('slice-detail-nav')).toBeVisible();
    await expect(page).toHaveURL(/\/slices\/root(\?.*)?$/);
  });

  test('navigating back to landing updates the URL path', async ({ page }) => {
    await page.goto('/slices/root');
    await expect(page.getByTestId('slice-detail-nav')).toBeVisible();

    // Click brand logo to go back to landing
    await page.getByRole('button', { name: /Git Slice/i }).click();
    await expect(page.getByRole('heading', { level: 1, name: landingTitle })).toBeVisible();
    expect(new URL(page.url()).pathname).toBe('/');
  });

  test('loading /slices directly opens the slices home', async ({ page }) => {
    await page.goto('/slices');
    await expect(page.getByTestId('slice-home-page')).toBeVisible();
  });

  test('loading / directly opens the landing page', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByRole('heading', { level: 1, name: landingTitle })).toBeVisible();
  });

  test('loading /docs directly opens the docs page', async ({ page }) => {
    await page.goto('/docs');
    await expect(page.getByRole('heading', { level: 1, name: /one versioned filesystem, two work surfaces\./i })).toBeVisible();
    await expect(page).toHaveURL(/\/docs$/);
  });

  test('legacy hash routes redirect to real paths', async ({ page }) => {
    await page.goto('/#/slices');
    await expect(page.getByTestId('slice-home-page')).toBeVisible();
    await expect(page).toHaveURL(/\/slices(\?.*)?$/);
  });

  test('reloading the repo browser page preserves the view', async ({ page }) => {
    await page.goto('/slices/root');
    await expect(page.getByTestId('slice-detail-nav')).toBeVisible();

    // Reload the page
    await page.reload();
    await expect(page.getByTestId('slice-detail-nav')).toBeVisible();
    await expect(page).toHaveURL(/\/slices\/root(\?.*)?$/);
  });

  test('brand button returns to landing from the browser', async ({ page }) => {
    await page.goto('/slices/root');
    await expect(page.getByTestId('slice-detail-nav')).toBeVisible();

    // Use brand button to return to landing
    await page.getByRole('button', { name: /Git Slice/i }).click();
    await expect(page.getByRole('heading', { level: 1, name: landingTitle })).toBeVisible();
  });

  test('browser route returns to browser from landing', async ({ page }) => {
    await page.goto('/slices/root');
    await expect(page.getByTestId('slice-detail-nav')).toBeVisible();

    // Back to landing via brand button
    await page.getByRole('button', { name: /Git Slice/i }).click();
    await expect(page.getByRole('heading', { level: 1, name: landingTitle })).toBeVisible();

    // Forward to browser via path route
    await page.goto('/slices/root');
    await expect(page.getByTestId('slice-detail-nav')).toBeVisible();
  });

  test('navigating to diff page updates the URL path', async ({ page }) => {
    await page.goto('/slices/root');

    await openGenesisReadme(page);

    await page.getByTestId('history-toggle').click();
    await expect(page.getByTestId('history-panel')).toBeVisible();

    const diffLink = page.getByTestId('commit-diff-link').first();
    await expect(diffLink).toBeVisible();
    await diffLink.click();

    await expect(page.getByTestId('commit-diff-page')).toBeVisible();
    expect(new URL(page.url()).pathname).toMatch(/^\/diff\/.+/);
  });

  test('reloading the diff page preserves the commit view', async ({ page }) => {
    await page.goto('/slices/root');

    await openGenesisReadme(page);

    await page.getByTestId('history-toggle').click();
    await expect(page.getByTestId('history-panel')).toBeVisible();

    await page.getByTestId('commit-diff-link').first().click();
    await expect(page.getByTestId('commit-diff-page')).toBeVisible();

    // Capture the URL, then reload
    const diffUrl = page.url();
    await page.reload();

    // Diff page should still be visible after reload
    await expect(page.getByTestId('commit-diff-page')).toBeVisible();
    expect(page.url()).toBe(diffUrl);
  });
});
