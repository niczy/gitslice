import test from 'node:test';
import assert from 'node:assert/strict';

import { startOAuthSignIn } from './auth.js';

const ORIGINAL_WINDOW = global.window;

function installWindow(urlString) {
  const assignedURLs = [];
  const url = new URL(urlString);
  global.window = {
    location: {
      href: url.toString(),
      origin: url.origin,
      assign(nextURL) {
        assignedURLs.push(nextURL);
      },
    },
  };
  return assignedURLs;
}

test.afterEach(() => {
  global.window = ORIGINAL_WINDOW;
});

test('startOAuthSignIn returns Clerk users to the current page', () => {
  const assignedURLs = installWindow('https://agenttools.dev/admin?tab=users#invite');

  startOAuthSignIn('clerk');

  assert.equal(assignedURLs.length, 1);
  const nextURL = new URL(assignedURLs[0]);
  assert.equal(nextURL.origin, 'https://agenttools.dev');
  assert.equal(nextURL.pathname, '/sign-in');
  assert.equal(
    nextURL.searchParams.get('redirect_url'),
    'https://agenttools.dev/admin?tab=users#invite',
  );
});

test('startOAuthSignIn falls back from auth pages to slices', () => {
  const assignedURLs = installWindow('https://agenttools.dev/sign-in/sso-callback?redirect_url=/admin');

  startOAuthSignIn('clerk');

  assert.equal(assignedURLs.length, 1);
  const nextURL = new URL(assignedURLs[0]);
  assert.equal(nextURL.pathname, '/sign-in');
  assert.equal(nextURL.searchParams.get('redirect_url'), 'https://agenttools.dev/slices');
});
