import { expect, test } from '@playwright/test';

const parseRgb = (value) => {
  const match = String(value).match(/rgba?\(([^)]+)\)/i);
  if (!match) {
    throw new Error(`Unsupported color value: ${value}`);
  }
  return match[1].split(',').slice(0, 3).map((part) => Number.parseFloat(part.trim()));
};

const relativeLuminance = ([red, green, blue]) => {
  const channels = [red, green, blue].map((channel) => {
    const value = channel / 255;
    return value <= 0.03928 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
  });
  return (0.2126 * channels[0]) + (0.7152 * channels[1]) + (0.0722 * channels[2]);
};

const contrastRatio = (foreground, background) => {
  const foregroundLuminance = relativeLuminance(parseRgb(foreground));
  const backgroundLuminance = relativeLuminance(parseRgb(background));
  const lighter = Math.max(foregroundLuminance, backgroundLuminance);
  const darker = Math.min(foregroundLuminance, backgroundLuminance);
  return (lighter + 0.05) / (darker + 0.05);
};

test('renders the docs page with navigation and core workflows', async ({ page }) => {
  await page.goto('/docs');

  await expect(page.getByRole('heading', { level: 1, name: /one versioned filesystem, two work surfaces\./i })).toBeVisible();
  await expect(page.getByRole('navigation', { name: /documentation navigation/i })).toBeVisible();
  await expect(page.getByRole('link', { name: /mental model/i })).toBeVisible();
  await expect(page.getByRole('heading', { name: /understand the system before choosing a workflow/i })).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /printf "hotfix shipped remotely\\n" \| gs fs write \/\$USER\/app\/NOTICE\.txt/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs slice create ui-refresh apps\/web/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs slice checkout <slice-id-or-slug>/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs repo import https:\/\/github\.com\/org\/repo\.git \/\$USER\/vendor\/repo --push-enabled/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs slice publish --message "refresh settings page" --files src\/routes\/settings\.tsx/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs slice publish --review-only --message "stage for review" --files src\/routes\/settings\.tsx/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs changeset show/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs fs search live --glob '\/\$USER\/app\/\*\*' --json/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs auth login --key ~\/\.config\/gitslice\/agent_ed25519/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs cache stats --checkouts/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs cache prune/i }).first()).toBeVisible();
  await expect(page.getByText(/uploads and checkouts exchange manifests first and then transfer only missing blocks/i)).toBeVisible();
  await expect(page.getByText(/Clerk and WorkOS are both supported/i)).toBeVisible();
  await expect(page.getByText(/Slice detail URLs track the selected directory or file/i)).toBeVisible();
});

test('docs page keeps readable contrast and avoids mobile overflow', async ({ page }) => {
  await page.goto('/docs');

  const samples = await page.evaluate(() => {
    const collect = (textSelector, surfaceSelector) => {
      const textElement = document.querySelector(textSelector);
      const surfaceElement = document.querySelector(surfaceSelector);
      if (!textElement || !surfaceElement) {
        throw new Error(`Missing docs contrast sample: ${textSelector} on ${surfaceSelector}`);
      }
      return {
        color: window.getComputedStyle(textElement).color,
        background: window.getComputedStyle(surfaceElement).backgroundColor,
      };
    };

    return [
      collect('.docs-card p', '.docs-card'),
      collect('.docs-code-card-head p', '.docs-code-card'),
      collect('.docs-command-card p', '.docs-command-card'),
      collect('.docs-page .code-block code', '.docs-page .code-block'),
    ];
  });

  for (const sample of samples) {
    expect(contrastRatio(sample.color, sample.background)).toBeGreaterThanOrEqual(4.5);
  }

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/docs');

  const horizontalOverflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(horizontalOverflow).toBeLessThanOrEqual(1);
});
