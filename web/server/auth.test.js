import test from 'node:test';
import assert from 'node:assert/strict';

import { __test, getProxyAuthorizationResult, handleAuthRequest, handleSessionRequest } from './auth.js';

const ORIGINAL_ENV = { ...process.env };
const ORIGINAL_FETCH = global.fetch;

function resetEnv() {
  for (const key of Object.keys(process.env)) {
    delete process.env[key];
  }
  Object.assign(process.env, ORIGINAL_ENV);
}

function configureClerkEnv() {
  process.env.AUTH_SECRET = 'test-auth-secret';
  process.env.AUTH_PROVIDER = 'clerk';
  process.env.CLERK_SECRET_KEY = 'sk_test_clerk';
  process.env.CLERK_PUBLISHABLE_KEY = 'pk_test_clerk';
  process.env.PUBLIC_WEB_BASE_URL = 'https://agenttools.dev';
}

async function createLocalSessionCookie(payload) {
  const value = await __test.createLocalSessionCookieValue(payload, process.env.AUTH_SECRET);
  return `gs_local_session=${value}`;
}

test.afterEach(() => {
  resetEnv();
  global.fetch = ORIGINAL_FETCH;
});

test('handleSessionRequest returns a cached local session for Clerk users', async () => {
  configureClerkEnv();
  global.fetch = async () => {
    throw new Error('unexpected fetch');
  };

  const future = new Date(Date.now() + 15 * 60 * 1000).toISOString();
  const refreshFuture = new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString();
  const cookie = await createLocalSessionCookie({
    source: 'clerk',
    sessionId: 'sess_cached',
    accessToken: 'access_cached',
    refreshToken: 'refresh_cached',
    accessTokenExpiresAt: future,
    refreshTokenExpiresAt: refreshFuture,
    user: {
      username: 'nic',
      name: 'Nic',
      email: 'nic@example.com',
      clerkUserId: 'user_123',
    },
  });

  const response = await handleSessionRequest(new Request('https://agenttools.dev/auth/session', {
    headers: { cookie },
  }));
  assert.equal(response.status, 200);
  const session = await response.json();
  assert.equal(session.source, 'clerk');
  assert.equal(session.apiAuthSource, 'local_session');
  assert.equal(session.user.username, 'nic');
  assert.equal(session.user.clerkUserId, 'user_123');
});

test('getProxyAuthorizationResult refreshes an expired local access token', async () => {
  configureClerkEnv();
  const expired = new Date(Date.now() - 5 * 60 * 1000).toISOString();
  const refreshFuture = new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString();
  const cookie = await createLocalSessionCookie({
    source: 'clerk',
    sessionId: 'sess_old',
    accessToken: 'access_old',
    refreshToken: 'refresh_old',
    accessTokenExpiresAt: expired,
    refreshTokenExpiresAt: refreshFuture,
    user: {
      username: 'nic',
      name: 'Nic',
      email: 'nic@example.com',
      clerkUserId: 'user_123',
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
  configureClerkEnv();
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

test('handleSessionRequest returns a cached local session for Clerk users', async () => {
  configureClerkEnv();
  global.fetch = async () => {
    throw new Error('unexpected fetch');
  };

  const future = new Date(Date.now() + 15 * 60 * 1000).toISOString();
  const refreshFuture = new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString();
  const cookie = await createLocalSessionCookie({
    source: 'clerk',
    sessionId: 'sess_clerk_cached',
    accessToken: 'access_clerk_cached',
    refreshToken: 'refresh_clerk_cached',
    accessTokenExpiresAt: future,
    refreshTokenExpiresAt: refreshFuture,
    user: {
      username: 'nic',
      name: 'Nic',
      email: 'nic@example.com',
      clerkUserId: 'user_clerk_123',
    },
  });

  const response = await handleSessionRequest(new Request('https://agenttools.dev/auth/session', {
    headers: { cookie },
  }));
  assert.equal(response.status, 200);
  const session = await response.json();
  assert.equal(session.source, 'clerk');
  assert.equal(session.apiAuthSource, 'local_session');
  assert.equal(session.user.username, 'nic');
  assert.equal(session.user.clerkUserId, 'user_clerk_123');
});

test('handleAuthRequest completes Clerk username using route auth context', async () => {
  configureClerkEnv();

  global.fetch = async (url, options = {}) => {
    assert.equal(url.toString(), 'http://localhost:50051/v1/auth/clerk/ensure-local-identity');
    assert.equal(options.method, 'POST');
    const payload = JSON.parse(String(options.body || '{}'));
    assert.equal(payload.preferredUsername, 'newclerkuser');
    assert.equal(payload.issueLocalSession, true);
    assert.ok(payload.signedClaims);
    return Response.json({
      accountId: 'acct_context',
      linkedExistingUser: false,
      localAuth: {
        sessionId: 'local_sess_context',
        accessToken: 'access_context',
        refreshToken: 'refresh_context',
        accessTokenExpiresAt: new Date(Date.now() + 15 * 60 * 1000).toISOString(),
        refreshTokenExpiresAt: new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString(),
        user: {
          username: 'newclerkuser',
          name: 'New Clerk User',
          primaryEmail: 'newclerkuser@example.com',
        },
      },
      user: {
        username: 'newclerkuser',
        name: 'New Clerk User',
        primaryEmail: 'newclerkuser@example.com',
      },
    });
  };

  const response = await handleAuthRequest(new Request('https://agenttools.dev/auth/clerk/complete-username', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: 'newclerkuser' }),
  }), {
    clerkAuth: {
      userId: 'user_context_123',
      sessionId: 'sess_context_123',
      orgId: '',
    },
    clerkUser: {
      id: 'user_context_123',
      fullName: 'New Clerk User',
      primaryEmailAddressId: 'email_context_123',
      emailAddresses: [
        { id: 'email_context_123', emailAddress: 'newclerkuser@example.com' },
      ],
      imageUrl: '',
    },
  });

  assert.equal(response.status, 200);
  assert.ok(response.headers.get('set-cookie')?.includes('gs_local_session='));
  const session = await response.json();
  assert.equal(session.source, 'clerk');
  assert.equal(session.apiAuthSource, 'local_session');
  assert.equal(session.user.username, 'newclerkuser');
  assert.equal(session.user.clerkUserId, 'user_context_123');
});
