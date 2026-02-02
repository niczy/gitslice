// @ts-check
import { test, expect } from '@playwright/test';

test.describe('Navigation history and URL reloading', () => {
  const waitForLanding = async (page) => {
    const landingHeading = page.getByRole('heading', { level: 1, name: /slice-based workflows/i });
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
    const browserTrigger = page.getByTestId('slice-dropdown-trigger');
    for (let i = 0; i < 6; i += 1) {
      if (await browserTrigger.isVisible()) {
        return;
      }
      await page.goForward();
      await page.waitForTimeout(200);
    }
    await expect(browserTrigger).toBeVisible();
  };

  test('navigating to repo browser updates the URL hash', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    await expect(page.getByTestId('slice-dropdown-trigger')).toBeVisible();
    await expect(page).toHaveURL(/#\/browser/);
  });

  test('navigating back to landing updates the URL hash', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();
    await expect(page.getByTestId('slice-dropdown-trigger')).toBeVisible();

    // Click brand logo to go back to landing
    await page.getByRole('button', { name: /Git Slice/i }).click();
    await expect(page.getByRole('heading', { level: 1, name: /slice-based workflows/i })).toBeVisible();
    expect(new URL(page.url()).hash).toBe('#/');
  });

  test('loading /#/browser directly opens the repo browser', async ({ page }) => {
    await page.goto('/#/browser');
    await expect(page.getByTestId('slice-dropdown-trigger')).toBeVisible();
  });

  test('loading /#/ directly opens the landing page', async ({ page }) => {
    await page.goto('/#/');
    await expect(page.getByRole('heading', { level: 1, name: /slice-based workflows/i })).toBeVisible();
  });

  test('reloading the repo browser page preserves the view', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();
    await expect(page.getByTestId('slice-dropdown-trigger')).toBeVisible();

    // Reload the page
    await page.reload();
    await expect(page.getByTestId('slice-dropdown-trigger')).toBeVisible();
    await expect(page).toHaveURL(/#\/browser/);
  });

  test('brand button returns to landing from the browser', async ({ page }) => {
    await page.goto('/');

    // Navigate landing -> browser
    await page.getByTestId('topbar-repo-browser').click();
    await expect(page.getByTestId('slice-dropdown-trigger')).toBeVisible();

    // Use brand button to return to landing
    await page.getByRole('button', { name: /Git Slice/i }).click();
    await expect(page.getByRole('heading', { level: 1, name: /slice-based workflows/i })).toBeVisible();
  });

  test('repo browser button returns to browser from landing', async ({ page }) => {
    await page.goto('/');

    // Navigate landing -> browser
    await page.getByTestId('topbar-repo-browser').click();
    await expect(page.getByTestId('slice-dropdown-trigger')).toBeVisible();

    // Back to landing via brand button
    await page.getByRole('button', { name: /Git Slice/i }).click();
    await expect(page.getByRole('heading', { level: 1, name: /slice-based workflows/i })).toBeVisible();

    // Forward to browser via repo button
    await page.getByTestId('topbar-repo-browser').click();
    await expect(page.getByTestId('slice-dropdown-trigger')).toBeVisible();
  });

  test('navigating to diff page updates the URL hash', async ({ page }) => {
    await page.goto('/#/browser');

    // Navigate to a file with history, then to the diff page
    await page.getByRole('button', { name: /📁.*o/i }).click();
    await expect(page.getByRole('button', { name: /📁.*genesis/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*genesis/i }).click();
    await expect(page.getByRole('button', { name: /📁.*projects/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*projects/i }).click();
    await expect(page.getByRole('button', { name: /📁.*gitslice/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*gitslice/i }).click();

    await expect(page.getByRole('button', { name: /README\.md/i })).toBeVisible();
    await page.getByRole('button', { name: /README\.md/i }).click();
    await expect(page.locator('.file-preview')).toBeVisible();

    await page.getByTestId('history-toggle').click();
    await expect(page.getByTestId('history-panel')).toBeVisible();

    const diffLink = page.getByTestId('commit-diff-link').first();
    await expect(diffLink).toBeVisible();
    await diffLink.click();

    await expect(page.getByTestId('commit-diff-page')).toBeVisible();
    expect(new URL(page.url()).hash).toMatch(/^#\/diff\/.+/);
  });

  test('reloading the diff page preserves the commit view', async ({ page }) => {
    await page.goto('/#/browser');

    // Navigate to diff page via file history
    await page.getByRole('button', { name: /📁.*o/i }).click();
    await expect(page.getByRole('button', { name: /📁.*genesis/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*genesis/i }).click();
    await expect(page.getByRole('button', { name: /📁.*projects/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*projects/i }).click();
    await expect(page.getByRole('button', { name: /📁.*gitslice/i })).toBeVisible();
    await page.getByRole('button', { name: /📁.*gitslice/i }).click();

    await expect(page.getByRole('button', { name: /README\.md/i })).toBeVisible();
    await page.getByRole('button', { name: /README\.md/i }).click();
    await expect(page.locator('.file-preview')).toBeVisible();

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
