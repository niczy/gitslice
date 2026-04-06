// @ts-check
import { test, expect } from '@playwright/test';

const webPort = process.env.E2E_WEB_PORT || '4173';
const webBaseURL = `http://127.0.0.1:${webPort}`;

test.describe('Cookie-backed web auth', () => {
  test('username login creates a persistent cookie-backed session', async ({ page }) => {
    await page.goto('/login');

    await expect(page.getByTestId('login-page')).toBeVisible();
    await page.getByLabel('Username').fill('webtester1');
    await page.getByRole('button', { name: /login with username/i }).click();

    await expect(page).toHaveURL(/\/browser(\?.*)?$/);
    await expect(page.getByTestId('topbar-profile')).toContainText('webtester1');
    await expect(page.getByTestId('slice-dropdown-trigger')).toContainText(/webtester1/i);
    await expect(page.getByRole('button', { name: /\+ Folder/i })).toHaveCount(0);
    await expect(page.getByRole('button', { name: /\+ File/i })).toHaveCount(0);

    await page.getByTestId('slice-dropdown-trigger').click();
    await expect(page.getByTestId('slice-dropdown-item').filter({ hasText: /root_slice|root slice/i })).toHaveCount(0);
    await page.keyboard.press('Escape');

    await page.getByTestId('topbar-settings').click();
    await expect(page.getByTestId('settings-page')).toBeVisible();
    await expect(page.getByText(/auth mode/i)).toBeVisible();
    await expect(page.getByText(/^dev$/i)).toBeVisible();

    await page.getByTestId('topbar-repos').click();
    await expect(page).toHaveURL(/\/browser(\?.*)?$/);
    await expect(page.getByTestId('slice-dropdown-trigger')).toContainText(/webtester1/i);

    await page.reload();
    await expect(page.getByTestId('slice-dropdown-trigger')).toBeVisible();
    await expect(page.getByTestId('topbar-profile')).toContainText('webtester1');
  });

  test('stale username-style browser slice param resolves to the home slice', async ({ page }) => {
    await page.goto('/login');
    await page.getByLabel('Username').fill('webtester2');
    await page.getByRole('button', { name: /login with username/i }).click();

    await expect(page).toHaveURL(/\/browser(\?.*)?$/);

    await page.goto('/browser?slice=webtester2');

    await expect(page.getByTestId('slice-dropdown-trigger')).toContainText(/webtester2/i);
    await expect(page.getByText(/Unable to load entries/i)).toHaveCount(0);
  });

  test('browser data requests stay same-origin and rely on the web proxy', async ({ page }) => {
    const seenBrowserRequests = [];
    page.on('request', (request) => {
      if (request.url().startsWith(`${webBaseURL}/v1/`)) {
        seenBrowserRequests.push(request.url());
      }
    });

    await page.goto('/login');
    await page.getByLabel('Username').fill('websplit1');
    await page.getByRole('button', { name: /login with username/i }).click();

    await expect(page).toHaveURL(/\/browser(\?.*)?$/);
    await page.getByTestId('topbar-settings').click();
    await expect(page.getByTestId('settings-page')).toBeVisible();
    await expect.poll(() => seenBrowserRequests.length).toBeGreaterThan(0);
  });

  test('settings shows repo bindings for the signed-in user', async ({ page }) => {
    const username = `webbindings${Date.now()}`;

    await page.goto('/login');
    await page.getByLabel('Username').fill(username);
    await page.getByRole('button', { name: /login with username/i }).click();
    await expect(page).toHaveURL(/\/browser(\?.*)?$/);

    await page.route('**/v1/repos/bindings', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({
          bindings: [
            {
              binding_id: 'binding-1',
              path: `/${username}/imports/demo`,
              repo_url: 'https://github.com/example/demo.git',
              branch: 'main',
              push_enabled: true,
              last_imported_commit: 'abc123',
              last_pushed_commit: 'def456',
            },
          ],
        }),
      });
    });

    await page.getByTestId('topbar-settings').click();
    await expect(page.getByTestId('settings-page')).toBeVisible();
    await expect(page.getByRole('heading', { name: /github bindings/i })).toBeVisible();
    await expect(page.getByTestId('settings-repo-bindings')).toContainText(`/${username}/imports/demo`);
    await expect(page.getByTestId('settings-repo-bindings')).toContainText('https://github.com/example/demo.git');
    await expect(page.getByTestId('settings-repo-bindings')).toContainText(/yes/i);
  });

  test('settings can add and revoke agent keys', async ({ page }) => {
    const username = `webagentkeys${Date.now()}`;
    const pastedPublicKey = 'AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA=';
    let keys = [
      {
        id: 'agk-primary',
        user_id: username,
        name: 'codex-laptop',
        algorithm: 'ed25519',
        fingerprint: 'ed25519:primary',
        created_at: '2026-03-24T00:00:00Z',
        updated_at: '2026-03-24T00:00:00Z',
        last_used_at: '',
        revoked_at: '',
        revoked: false,
      },
    ];

    await page.goto('/login');
    await page.getByLabel('Username').fill(username);
    await page.getByRole('button', { name: /login with username/i }).click();
    await expect(page).toHaveURL(/\/browser(\?.*)?$/);

    await page.route('**/v1/repos/bindings', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ bindings: [] }),
      });
    });
    await page.route('**/v1/auth/agent/keys', async (route) => {
      if (route.request().method() === 'GET') {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ keys }),
        });
        return;
      }
      if (route.request().method() === 'POST') {
        const payload = JSON.parse(route.request().postData() || '{}');
        expect(payload.name).toBe('ci-runner');
        expect(payload.algorithm).toBe('ed25519');
        expect(payload.publicKey).toBe(pastedPublicKey);
        const created = {
          id: 'agk-ci',
          user_id: username,
          name: payload.name,
          algorithm: payload.algorithm,
          fingerprint: 'ed25519:ci',
          created_at: '2026-03-24T00:05:00Z',
          updated_at: '2026-03-24T00:05:00Z',
          last_used_at: '',
          revoked_at: '',
          revoked: false,
        };
        keys = [...keys, created];
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify(created),
        });
      }
    });
    await page.route('**/v1/auth/agent/keys/*', async (route) => {
      const id = route.request().url().split('/').pop();
      keys = keys.map((key) => (
        key.id === id
          ? { ...key, revoked: true, revoked_at: '2026-03-24T00:06:00Z' }
          : key
      ));
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: '{}',
      });
    });

    await page.getByTestId('topbar-settings').click();
    await expect(page.getByTestId('settings-page')).toBeVisible();
    await expect(page.getByTestId('settings-agent-keys')).toContainText('codex-laptop');
    await expect(page.getByTestId('settings-agent-keys')).toContainText('ed25519:primary');

    await page.getByTestId('settings-agent-key-name').fill('ci-runner');
    await page.getByTestId('settings-agent-key-public-key').fill(pastedPublicKey);
    await page.getByTestId('settings-agent-key-submit').click();

    await expect(page.getByTestId('settings-agent-keys')).toContainText('ci-runner');
    await expect(page.getByTestId('settings-agent-keys')).toContainText('ed25519:ci');

    await page.getByTestId('settings-agent-key-revoke-agk-ci').click();
    await expect(page.getByTestId('settings-agent-keys')).toContainText(/revoked/i);
    await expect(page.getByTestId('settings-agent-key-revoke-agk-ci')).toBeDisabled();
  });

  test('settings shows and revokes local gitslice sessions', async ({ page }) => {
    const username = `websessions${Date.now()}`;
    let sessions = [
      {
        id: 'sess-current',
        user_id: username,
        device_info: 'Firefox on macOS',
        last_seen_at: '2026-04-05T12:00:00Z',
        current: true,
        agent_key_id: '',
      },
      {
        id: 'sess-cli',
        user_id: username,
        device_info: 'gs auth login --device',
        last_seen_at: '2026-04-05T11:00:00Z',
        current: false,
        agent_key_id: '',
      },
    ];

    await page.goto('/login');
    await page.getByLabel('Username').fill(username);
    await page.getByRole('button', { name: /login with username/i }).click();
    await expect(page).toHaveURL(/\/browser(\?.*)?$/);

    await page.route('**/v1/repos/bindings', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ bindings: [] }),
      });
    });
    await page.route('**/v1/auth/sessions', async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ sessions }),
      });
    });
    await page.route('**/v1/auth/sessions/*', async (route) => {
      const revokedId = route.request().url().split('/').pop();
      sessions = sessions.filter((session) => session.id !== revokedId);
      await route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: '{}',
      });
    });

    await page.getByTestId('topbar-settings').click();
    await expect(page.getByTestId('settings-page')).toBeVisible();
    await expect(page.getByTestId('settings-sessions')).toContainText('Firefox on macOS');
    await expect(page.getByTestId('settings-sessions')).toContainText('gs auth login --device');
    await expect(page.getByTestId('settings-sessions')).toContainText(/current/i);

    await page.getByTestId('settings-session-revoke-sess-cli').click();
    await expect(page.getByTestId('settings-sessions')).not.toContainText('gs auth login --device');
  });

  test('profile can update local fields and delete the local account', async ({ page }) => {
    const username = `webprofile${Date.now()}`;
    let currentUser = {
      id: username,
      username,
      name: 'Original Name',
      primary_email: 'original@example.com',
      created_at: '2026-04-05T10:00:00Z',
    };
    let organizations = [
      {
        id: 'org-1',
        slug: 'acme',
        name: 'Acme',
        owner_user_id: username,
      },
    ];
    let deleted = false;

    await page.goto('/login');
    await page.getByLabel('Username').fill(username);
    await page.getByRole('button', { name: /login with username/i }).click();
    await expect(page).toHaveURL(/\/browser(\?.*)?$/);

    await page.route('**/v1/users/me', async (route) => {
      if (route.request().method() === 'GET') {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify(currentUser),
        });
        return;
      }
      if (route.request().method() === 'PATCH') {
        const payload = JSON.parse(route.request().postData() || '{}');
        currentUser = {
          ...currentUser,
          name: payload.name,
          primary_email: payload.primaryEmail,
        };
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify(currentUser),
        });
        return;
      }
      if (route.request().method() === 'DELETE') {
        deleted = true;
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: '{}',
        });
      }
    });
    await page.route('**/v1/orgs', async (route) => {
      if (route.request().method() === 'GET') {
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ organizations }),
        });
        return;
      }
      if (route.request().method() === 'POST') {
        const payload = JSON.parse(route.request().postData() || '{}');
        organizations = [...organizations, {
          id: 'org-2',
          slug: 'new-org',
          name: payload.name,
          owner_user_id: username,
        }];
        await route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify(organizations[organizations.length - 1]),
        });
      }
    });

    await page.getByTestId('topbar-settings').click();
    await page.getByRole('button', { name: /open profile details/i }).click();
    await expect(page.getByTestId('profile-page')).toBeVisible();
    await expect(page.getByLabel(/^Name$/)).toHaveValue('Original Name');
    await expect(page.getByLabel(/^Primary email$/)).toHaveValue('original@example.com');
    await expect(page.locator('.org-name', { hasText: 'Acme' })).toBeVisible();

    await page.getByLabel(/^Name$/).fill('Updated Name');
    await page.getByLabel(/^Primary email$/).fill('updated@example.com');
    await page.getByRole('button', { name: /save profile/i }).click();
    await expect(page.getByText(/profile updated/i)).toBeVisible();

    await page.getByLabel(/type your username to confirm/i).fill(username);
    await page.getByRole('button', { name: /delete account/i }).click();
    await expect.poll(() => deleted).toBe(true);
    await expect(page).toHaveURL(/\/$/);
  });
});
