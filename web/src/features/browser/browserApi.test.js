import assert from 'node:assert/strict';
import test from 'node:test';

import {
  buildBrowserEntriesUrl,
  buildBrowserFileHistoryUrl,
  buildBrowserFileUrl,
  buildBrowserRawFileUrl,
  normalizeWorkspaceResultPath,
  readBrowserErrorMessage,
} from './browserApi.js';

test('normalizeWorkspaceResultPath removes leading slashes', () => {
  assert.equal(normalizeWorkspaceResultPath('/src/App.jsx'), 'src/App.jsx');
  assert.equal(normalizeWorkspaceResultPath('src/App.jsx'), 'src/App.jsx');
  assert.equal(normalizeWorkspaceResultPath(null), '');
});

test('browser API URL helpers encode path segments and preserve slice hash query', () => {
  assert.equal(
    buildBrowserEntriesUrl({
      apiBaseUrl: 'https://api.example.test',
      sliceId: 'slice_123',
      path: 'dir/a b',
      sliceHash: 'hash/one',
    }),
    'https://api.example.test/v1/slices/slice_123/entries/dir/a%20b?slice_version.slice_hash=hash%2Fone',
  );

  assert.equal(
    buildBrowserFileUrl({
      apiBaseUrl: '',
      sliceId: 'slice_123',
      filePath: 'dir/#file.txt',
      sliceHash: '',
    }),
    '/v1/slices/slice_123/files/dir/%23file.txt',
  );

  assert.equal(
    buildBrowserRawFileUrl({
      sliceId: 'slice 123',
      filePath: 'dir/a b.txt',
      sliceHash: 'hash/one',
    }),
    '/raw/slices/slice%20123/dir/a%20b.txt?slice_version.slice_hash=hash%2Fone',
  );

  assert.equal(
    buildBrowserFileHistoryUrl({
      apiBaseUrl: '/api',
      sliceId: 'slice_123',
      filePath: 'dir/a b.txt',
    }),
    '/api/v1/slices/slice_123/files/history/dir/a%20b.txt',
  );
});

test('readBrowserErrorMessage prefers structured API details', async () => {
  const response = new Response(JSON.stringify({ message: 'missing file' }), { status: 404 });
  assert.equal(await readBrowserErrorMessage(response, 'Unable to load file'), 'Unable to load file: missing file');
});

test('readBrowserErrorMessage falls back to status when details are empty', async () => {
  const response = new Response('', { status: 500 });
  assert.equal(await readBrowserErrorMessage(response, 'Unable to load file'), 'Unable to load file (500)');
});
