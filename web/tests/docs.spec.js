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
  const docsNav = page.getByRole('navigation', { name: /documentation navigation/i });
  await expect(docsNav).toBeVisible();
  await expect(docsNav.getByRole('link', { name: /mental model/i })).toBeVisible();
  await expect(page.getByRole('heading', { name: /understand the system before choosing a workflow/i })).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /printf "hotfix shipped remotely\\n" \| gs fs write \/\$USER\/app\/NOTICE\.txt/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs slice create ui-refresh apps\/web/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs slice checkout <slice-id-or-slug>/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs repo import https:\/\/github\.com\/org\/repo\.git \/\$USER\/vendor\/repo/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs slice export --message "refresh settings page" --files src\/routes\/settings\.tsx/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs changeset merge/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs slice publish --review-only --message "stage for review" --files src\/routes\/settings\.tsx/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs changeset show/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs fs search live --glob '\/\$USER\/app\/\*\*' --json/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs auth login --key ~\/\.config\/gitslice\/agent_ed25519/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs agent start --dir \/path\/to\/agent-workspaces/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs agent run --dir \/path\/to\/agent-workspaces/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs agent input <session-id> "summarize the current diff"/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs cache stats --checkouts/i }).first()).toBeVisible();
  await expect(page.locator('code').filter({ hasText: /gs cache prune/i }).first()).toBeVisible();
  await expect(page.getByText(/uploads and checkouts exchange manifests first and then transfer only missing blocks/i)).toBeVisible();
  await expect(page.getByText(/checks out each session's slice into its own subdirectory/i)).toBeVisible();
  await expect(page.getByText(/Both commands use the current directory by default/i)).toBeVisible();
  await expect(page.getByText(/hosted browser auth through Clerk/i)).toBeVisible();
  await expect(page.getByText(/Slice detail URLs track the selected directory or file/i)).toBeVisible();
  await expect(page.locator('#quick-start .markdown-heading-link')).toHaveAttribute('href', '#quick-start');
  await expect(page.locator('#local-agent-sessions .markdown-heading-link')).toHaveAttribute('href', '#local-agent-sessions');
  await expect(page.locator('#auth .markdown-heading-link')).toHaveAttribute('href', '#auth');

  await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
  await expect(docsNav).toBeInViewport();
  await expect(page.locator('.docs-nav-shell')).toHaveCSS('position', 'sticky');

  await docsNav.getByRole('link', { name: /mental model/i }).click();
  await expect(page).toHaveURL(/#mental-model$/);
  await expect.poll(async () => page.locator('#mental-model').evaluate((element) => element.getBoundingClientRect().top)).toBeGreaterThanOrEqual(80);
  await expect.poll(async () => page.locator('#mental-model').evaluate((element) => element.getBoundingClientRect().top)).toBeLessThanOrEqual(140);
});

test('serves docs.md as the docs source', async ({ page }) => {
  const response = await page.goto('/docs.md');

  expect(response?.status()).toBe(200);
  expect(response?.headers()['content-type']).toContain('text/markdown');
  await expect(page.locator('body')).toContainText('# One versioned filesystem, two work surfaces.');
  await expect(page.locator('body')).toContainText('The docs page is rendered from `/docs.md`');
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
      collect('.docs-hero-copy .lede', 'body'),
      collect('.docs-nav-link', '.docs-nav-card'),
      collect('.docs-markdown p', 'body'),
      collect('.docs-markdown pre code', '.docs-markdown pre'),
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
