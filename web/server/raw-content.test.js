import test from 'node:test';
import assert from 'node:assert/strict';

import { handleRawContentRequest } from './raw-content.js';

const ORIGINAL_ENV = { ...process.env };
const ORIGINAL_FETCH = global.fetch;

function resetEnv() {
  for (const key of Object.keys(process.env)) {
    delete process.env[key];
  }
  Object.assign(process.env, ORIGINAL_ENV);
}

function configureEnv() {
  process.env.PUBLIC_API_BASE_URL = 'http://api.test';
  process.env.AUTH_PROVIDER = 'none';
}

test.afterEach(() => {
  resetEnv();
  global.fetch = ORIGINAL_FETCH;
});

test('handleRawContentRequest serves public file bytes from the JSON file API', async () => {
  configureEnv();
  global.fetch = async (url, options = {}) => {
    assert.equal(url.toString(), 'http://api.test/v1/public/files/scripts/install.sh?slice_id=sl_123');
    assert.equal(options.method, 'GET');
    assert.equal(options.headers.get('Accept'), 'application/json');
    return Response.json({
      file: {
        path: 'scripts/install.sh',
        content: btoa('echo hello\n'),
        size: 11,
        hash: 'hash123',
      },
    });
  };

  const request = new Request('https://agenttools.dev/raw/public/scripts/install.sh?slice_id=sl_123');
  const response = await handleRawContentRequest(request, 'public/scripts/install.sh');

  assert.equal(response.status, 200);
  assert.equal(response.headers.get('Content-Type'), 'text/plain; charset=utf-8');
  assert.equal(response.headers.get('ETag'), '"hash123"');
  assert.equal(response.headers.get('X-Content-Type-Options'), 'nosniff');
  assert.equal(response.headers.get('Cache-Control'), 'public, no-cache');
  assert.equal(await response.text(), 'echo hello\n');
});

test('handleRawContentRequest supports slice raw URLs and forwards version query params', async () => {
  configureEnv();
  global.fetch = async (url) => {
    assert.equal(
      url.toString(),
      'http://api.test/v1/slices/sl-123/files/src/main.go?slice_version.slice_hash=abc',
    );
    return Response.json({
      file: {
        path: 'src/main.go',
        content: btoa('package main\n'),
        hash: 'go-hash',
      },
    });
  };

  const request = new Request('https://agenttools.dev/raw/slices/sl-123/src/main.go?slice_version.slice_hash=abc', {
    headers: { Authorization: 'User nic' },
  });
  const response = await handleRawContentRequest(request, 'slices/sl-123/src/main.go');

  assert.equal(response.status, 200);
  assert.equal(response.headers.get('Cache-Control'), 'private, no-store');
  assert.equal(response.headers.get('Vary'), 'Authorization, Cookie');
  assert.equal(await response.text(), 'package main\n');
});

test('handleRawContentRequest uses public visibility for anonymous slice raw URLs', async () => {
  configureEnv();
  const calls = [];
  global.fetch = async (url) => {
    calls.push(url.toString());
    return Response.json({
      file: {
        path: 'README.md',
        content: btoa('# Public\n'),
        hash: 'public-hash',
      },
    });
  };

  const request = new Request('https://agenttools.dev/raw/slices/sl-123/README.md');
  const response = await handleRawContentRequest(request, 'slices/sl-123/README.md');

  assert.deepEqual(calls, [
    'http://api.test/v1/public/files/README.md?slice_id=sl-123',
  ]);
  assert.equal(response.status, 200);
  assert.equal(response.headers.get('Cache-Control'), 'public, no-cache');
  assert.equal(await response.text(), '# Public\n');
});

test('handleRawContentRequest falls back to public files when authenticated access is unavailable', async () => {
  configureEnv();
  const calls = [];
  global.fetch = async (url) => {
    calls.push(url.toString());
    if (calls.length === 1) {
      return Response.json({ error: 'Not signed in' }, { status: 401 });
    }
    return Response.json({
      file: {
        path: 'README.md',
        content: btoa('# Public\n'),
        hash: 'public-hash',
      },
    });
  };

  const request = new Request('https://agenttools.dev/raw/slices/sl-123/README.md', {
    headers: { Authorization: 'User nic' },
  });
  const response = await handleRawContentRequest(request, 'slices/sl-123/README.md');

  assert.deepEqual(calls, [
    'http://api.test/v1/slices/sl-123/files/README.md',
    'http://api.test/v1/public/files/README.md?slice_id=sl-123',
  ]);
  assert.equal(response.status, 200);
  assert.equal(response.headers.get('Cache-Control'), 'public, no-cache');
  assert.equal(await response.text(), '# Public\n');
});

test('handleRawContentRequest returns 304 for matching raw ETags', async () => {
  configureEnv();
  global.fetch = async () => Response.json({
    file: {
      path: 'README.md',
      content: btoa('# Public\n'),
      hash: 'public-hash',
    },
  });

  const request = new Request('https://agenttools.dev/raw/public/README.md?slice_id=sl-123', {
    headers: { 'If-None-Match': '"public-hash"' },
  });
  const response = await handleRawContentRequest(request, 'public/README.md');

  assert.equal(response.status, 304);
  assert.equal(response.headers.get('ETag'), '"public-hash"');
  assert.equal(await response.text(), '');
});

test('handleRawContentRequest handles HEAD without a response body', async () => {
  configureEnv();
  global.fetch = async () => Response.json({
    file: {
      path: 'README.md',
      content: btoa('# Public\n'),
      hash: 'public-hash',
    },
  });

  const request = new Request('https://agenttools.dev/raw/public/README.md?slice_id=sl-123', {
    method: 'HEAD',
  });
  const response = await handleRawContentRequest(request, 'public/README.md');

  assert.equal(response.status, 200);
  assert.equal(response.headers.get('Content-Length'), '9');
  assert.equal(await response.text(), '');
});
