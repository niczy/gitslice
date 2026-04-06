import test from 'node:test';
import assert from 'node:assert/strict';

import { __test, getProxyAuthorizationResult, handleSessionRequest } from './auth.js';

const ORIGINAL_ENV = { ...process.env };
const ORIGINAL_FETCH = global.fetch;

function resetEnv() {
  for (const key of Object.keys(process.env)) {
    delete process.env[key];
  }
  Object.assign(process.env, ORIGINAL_ENV);
}

function configureWorkOSEnv() {
  process.env.AUTH_SECRET = 'test-auth-secret';
  process.env.AUTH_PROVIDER = 'workos';
  process.env.WORKOS_CLIENT_ID = 'client_test';
  process.env.WORKOS_API_KEY = 'sk_test';
  process.env.WORKOS_COOKIE_PASSWORD = 'cookie-password';
  process.env.WORKOS_REDIRECT_URI = 'https://agenttools.dev/auth/callback/workos';
}

async function createLocalSessionCookie(payload) {
  const value = await __test.createLocalSessionCookieValue(payload, process.env.AUTH_SECRET);
  return `gs_local_session=${value}`;
}

test.afterEach(() => {
  resetEnv();
  global.fetch = ORIGINAL_FETCH;
});

test('handleSessionRequest returns a cached local session for WorkOS users', async () => {
  configureWorkOSEnv();
  global.fetch = async () => {
    throw new Error('unexpected fetch');
  };

  const future = new Date(Date.now() + 15 * 60 * 1000).toISOString();
  const refreshFuture = new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString();
  const cookie = await createLocalSessionCookie({
    sessionId: 'sess_cached',
    accessToken: 'access_cached',
    refreshToken: 'refresh_cached',
    accessTokenExpiresAt: future,
    refreshTokenExpiresAt: refreshFuture,
    user: {
      username: 'nic',
      name: 'Nic',
      email: 'nic@example.com',
      workosUserId: 'user_123',
    },
  });

  const response = await handleSessionRequest(new Request('https://agenttools.dev/auth/session', {
    headers: { cookie },
  }));
  assert.equal(response.status, 200);
  const session = await response.json();
  assert.equal(session.source, 'workos');
  assert.equal(session.apiAuthSource, 'local_session');
  assert.equal(session.user.username, 'nic');
  assert.equal(session.user.workosUserId, 'user_123');
});

test('getProxyAuthorizationResult refreshes an expired local access token', async () => {
  configureWorkOSEnv();
  const expired = new Date(Date.now() - 5 * 60 * 1000).toISOString();
  const refreshFuture = new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString();
  const cookie = await createLocalSessionCookie({
    sessionId: 'sess_old',
    accessToken: 'access_old',
    refreshToken: 'refresh_old',
    accessTokenExpiresAt: expired,
    refreshTokenExpiresAt: refreshFuture,
    user: {
      username: 'nic',
      name: 'Nic',
      email: 'nic@example.com',
      workosUserId: 'user_123',
    },
  });

  global.fetch = async (url, options = {}) => {
    assert.equal(url.toString(), 'http://localhost:50051/v1/auth/token/refresh');
    assert.equal(options.method, 'POST');
    const payload = JSON.parse(String(options.body || '{}'));
    assert.equal(payload.refreshToken, 'refresh_old');
    return Response.json({
      sessionId: 'sess_new',
      accessToken: 'access_new',
      refreshToken: 'refresh_new',
      accessTokenExpiresAt: new Date(Date.now() + 20 * 60 * 1000).toISOString(),
      refreshTokenExpiresAt: new Date(Date.now() + 48 * 60 * 60 * 1000).toISOString(),
      user: {
        username: 'nic',
        name: 'Nic',
        primaryEmail: 'nic@example.com',
      },
    });
  };

  const authResult = await getProxyAuthorizationResult(new Request('https://agenttools.dev/v1/slices?limit=200', {
    headers: { cookie },
  }));
  assert.equal(authResult.authorization, 'Bearer access_new');
  assert.equal(authResult.rejectUnauthenticated, false);
  assert.ok(authResult.setCookies.some((value) => value.includes('gs_local_session=')));
});

test('getProxyAuthorizationResult clears malformed local session cookies', async () => {
  configureWorkOSEnv();
  global.fetch = async () => {
    throw new Error('unexpected fetch');
  };

  const authResult = await getProxyAuthorizationResult(new Request('https://agenttools.dev/v1/slices?limit=200', {
    headers: { cookie: 'gs_local_session=malformed' },
  }));
  assert.equal(authResult.authorization, '');
  assert.equal(authResult.rejectUnauthenticated, true);
  assert.ok(authResult.setCookies.some((value) => value.startsWith('gs_local_session=')));
  assert.ok(authResult.setCookies.some((value) => value.includes('Max-Age=0')));
});
