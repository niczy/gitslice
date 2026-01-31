// @ts-check
import { test, expect } from '@playwright/test';

test.describe('Navigation history and URL reloading', () => {
  test('navigating to repo browser updates the URL hash', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();

    await expect(page.getByRole('heading', { name: /Browse the fetched code/i })).toBeVisible();
    await expect(page).toHaveURL(/#\/browser\?slice=/);
  });

  test('navigating back to landing updates the URL hash', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();
    await expect(page.getByRole('heading', { name: /Browse the fetched code/i })).toBeVisible();

    // Click brand logo to go back to landing
    await page.getByRole('button', { name: /Git Slice/i }).click();
    await expect(page.getByRole('heading', { level: 1, name: /slice-based workflows/i })).toBeVisible();
    expect(new URL(page.url()).hash).toBe('#/');
  });

  test('loading /#/browser directly opens the repo browser', async ({ page }) => {
    await page.goto('/#/browser');
    await expect(page.getByRole('heading', { name: /Browse the fetched code/i })).toBeVisible();
  });

  test('loading /#/ directly opens the landing page', async ({ page }) => {
    await page.goto('/#/');
    await expect(page.getByRole('heading', { level: 1, name: /slice-based workflows/i })).toBeVisible();
  });

  test('reloading the repo browser page preserves the view', async ({ page }) => {
    await page.goto('/');
    await page.getByTestId('topbar-repo-browser').click();
    await expect(page.getByRole('heading', { name: /Browse the fetched code/i })).toBeVisible();

    // Reload the page
    await page.reload();
    await expect(page.getByRole('heading', { name: /Browse the fetched code/i })).toBeVisible();
    await expect(page).toHaveURL(/#\/browser\?slice=/);
  });

  test('browser back button navigates to the previous page', async ({ page }) => {
    await page.goto('/');

    // Navigate landing -> browser
    await page.getByTestId('topbar-repo-browser').click();
    await expect(page.getByRole('heading', { name: /Browse the fetched code/i })).toBeVisible();

    // Press browser back
    await page.goBack();
    await page.goBack();
    await expect(page.getByRole('heading', { level: 1, name: /slice-based workflows/i })).toBeVisible();
  });

  test('browser forward button navigates forward', async ({ page }) => {
    await page.goto('/');

    // Navigate landing -> browser
    await page.getByTestId('topbar-repo-browser').click();
    await expect(page.getByRole('heading', { name: /Browse the fetched code/i })).toBeVisible();

    // Back to landing
    await page.goBack();
    await page.goBack();
    await expect(page.getByRole('heading', { level: 1, name: /slice-based workflows/i })).toBeVisible();

    // Forward to browser
    await page.goForward();
    await page.goForward();
    await expect(page.getByRole('heading', { name: /Browse the fetched code/i })).toBeVisible();
  });

  test('navigating to diff page updates the URL hash', async ({ page }) => {
    await page.goto('/#/browser');

    // Navigate to a file with history, then to the diff page
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
