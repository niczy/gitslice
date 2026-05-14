import assert from 'node:assert/strict';
import test from 'node:test';

import {
  getDirectoryAncestorPaths,
  getEntryDisplayPath,
  getEntryName,
  getFilePayloadSize,
  getNumericFileSize,
  getParentDirectoryPath,
  getPreviewMeta,
  getTreeFileSize,
  sortEntriesByTypeAndName,
} from './browserModel.js';

test('entry helpers normalize names and display paths', () => {
  assert.equal(getEntryName({ name: ' README.md ', path: '/docs/readme.md' }), 'README.md');
  assert.equal(getEntryName({ path: '/docs/readme.md' }), 'readme.md');
  assert.equal(getEntryName({ path: '/' }), '/');
  assert.equal(getEntryDisplayPath({ path: 'docs/readme.md' }), '/docs/readme.md');
  assert.equal(getEntryDisplayPath({ path: '' }), '/');
});

test('sortEntriesByTypeAndName sorts directories first by case-insensitive name', () => {
  const sorted = sortEntriesByTypeAndName([
    { type: 'ENTRY_TYPE_FILE', path: 'z.txt' },
    { type: 'ENTRY_TYPE_DIRECTORY', path: 'beta' },
    { type: 'ENTRY_TYPE_FILE', path: 'A.txt' },
    { type: 'ENTRY_TYPE_DIRECTORY', path: 'alpha' },
  ]);

  assert.deepEqual(sorted.map((entry) => entry.path), ['alpha', 'beta', 'A.txt', 'z.txt']);
});

test('file size helpers prefer explicit sizes and fall back to decoded content length', () => {
  assert.equal(getNumericFileSize('42'), 42);
  assert.equal(getNumericFileSize(-1), null);
  assert.equal(getNumericFileSize('bad'), null);
  assert.equal(getFilePayloadSize({ size: '12' }, 'content'), 12);
  assert.equal(getFilePayloadSize({}, 'content'), 7);
  assert.equal(getFilePayloadSize({}, ''), null);
});

test('getTreeFileSize resolves file entries from their parent directory', () => {
  const treeEntries = {
    '': [
      { type: 'ENTRY_TYPE_DIRECTORY', path: 'src' },
      { type: 'ENTRY_TYPE_FILE', path: 'README.md', size: '10' },
    ],
    src: [
      { type: 'ENTRY_TYPE_FILE', path: 'src/App.jsx', size: 128 },
      { type: 'ENTRY_TYPE_DIRECTORY', path: 'src/components', size: 999 },
    ],
  };

  assert.equal(getTreeFileSize(treeEntries, 'README.md'), 10);
  assert.equal(getTreeFileSize(treeEntries, '/src/App.jsx'), 128);
  assert.equal(getTreeFileSize(treeEntries, 'src/components'), null);
  assert.equal(getTreeFileSize(treeEntries, ''), null);
});

test('path helpers return ancestors and parent directories', () => {
  assert.deepEqual(getDirectoryAncestorPaths('src/components/Button.jsx'), [
    '',
    'src',
    'src/components',
    'src/components/Button.jsx',
  ]);
  assert.equal(getParentDirectoryPath('src/components/Button.jsx'), 'src/components');
  assert.equal(getParentDirectoryPath('README.md'), '');
});

test('getPreviewMeta selects preview mode from extension', () => {
  assert.deepEqual(getPreviewMeta('image.png', 'abc'), {
    mode: 'image',
    src: 'data:image/png;base64,abc',
  });
  assert.deepEqual(getPreviewMeta('doc.pdf', 'abc'), {
    mode: 'pdf',
    src: 'data:application/pdf;base64,abc',
  });
  assert.deepEqual(getPreviewMeta('README.md', 'abc'), { mode: 'markdown', src: '' });
  assert.deepEqual(getPreviewMeta('main.go', 'abc'), { mode: 'text', src: '' });
});
