import test from 'node:test';
import assert from 'node:assert/strict';

import { proxyRequest } from './proxy.js';

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
  process.env.PUBLIC_API_BASE_URL = 'http://api.test';
  process.env.PUBLIC_WEB_BASE_URL = 'https://agenttools.dev';
}

test.afterEach(() => {
  resetEnv();
  global.fetch = ORIGINAL_FETCH;
});

test('proxyRequest exchanges a Clerk bearer token for a local API session', async () => {
  configureClerkEnv();
  const calls = [];
  global.fetch = async (url, options = {}) => {
    calls.push(url.toString());
    if (url.toString() === 'http://api.test/v1/auth/clerk/ensure-local-identity') {
      return Response.json({
        localAuth: {
          sessionId: 'local_sess_proxy',
          accessToken: 'local_access_proxy',
          refreshToken: 'local_refresh_proxy',
          accessTokenExpiresAt: new Date(Date.now() + 15 * 60 * 1000).toISOString(),
          refreshTokenExpiresAt: new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString(),
          user: {
            username: 'proxyuser',
            name: 'Proxy User',
            primaryEmail: 'proxyuser@example.com',
          },
        },
        user: {
          username: 'proxyuser',
          name: 'Proxy User',
          primaryEmail: 'proxyuser@example.com',
        },
      });
    }
    assert.equal(url.toString(), 'http://api.test/v1/slices?limit=200');
    assert.equal(options.headers.get('Authorization'), 'Bearer local_access_proxy');
    return Response.json({ slices: [] });
  };

  const response = await proxyRequest(new Request('https://agenttools.dev/v1/slices?limit=200', {
    headers: { Authorization: 'Bearer clerk_session_token' },
  }), '/v1/slices', {
    clerkAuth: {
      userId: 'user_proxy_123',
      sessionId: 'sess_proxy_123',
    },
    clerkUser: {
      id: 'user_proxy_123',
      fullName: 'Proxy User',
      primaryEmailAddressId: 'email_proxy_123',
      emailAddresses: [
        { id: 'email_proxy_123', emailAddress: 'proxyuser@example.com' },
      ],
    },
  });

  assert.equal(response.status, 200);
  assert.deepEqual(calls, [
    'http://api.test/v1/auth/clerk/ensure-local-identity',
    'http://api.test/v1/slices?limit=200',
  ]);
});

test('proxyRequest rejects an unresolved Clerk bearer token before proxying', async () => {
  configureClerkEnv();
  const calls = [];
  global.fetch = async (url) => {
    calls.push(url.toString());
    if (url.toString() === 'http://api.test/v1/auth/clerk/ensure-local-identity') {
      return Response.json({ error: 'username required' }, { status: 409 });
    }
    throw new Error(`unexpected upstream request: ${url}`);
  };

  const response = await proxyRequest(new Request('https://agenttools.dev/v1/agent-sessions/sess_123/events', {
    method: 'POST',
    headers: {
      Authorization: 'Bearer clerk_session_token',
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      stream: 'control',
      type: 'local_changes_requested',
      payload: '',
    }),
  }), '/v1/agent-sessions/sess_123/events', {
    clerkAuth: {
      userId: 'user_proxy_123',
      sessionId: 'sess_proxy_123',
    },
    clerkUser: {
      id: 'user_proxy_123',
      fullName: 'Proxy User',
      primaryEmailAddressId: 'email_proxy_123',
      emailAddresses: [
        { id: 'email_proxy_123', emailAddress: 'proxyuser@example.com' },
      ],
    },
  });

  assert.equal(response.status, 401);
  assert.deepEqual(await response.json(), { error: 'Not signed in' });
  assert.deepEqual(calls, [
    'http://api.test/v1/auth/clerk/ensure-local-identity',
  ]);
});
