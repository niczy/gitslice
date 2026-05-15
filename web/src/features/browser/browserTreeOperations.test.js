import assert from 'node:assert/strict';
import test from 'node:test';

import {
  encodeTreeTextContent,
  filterVisibleTreeEntries,
  getDirectoryMarkerPath,
  getSliceWriteRoots,
  getTreeCreateBlockedReason,
  isSuccessfulMergeResponse,
  joinTreePath,
  normalizeChangesetId,
  normalizeTreeOperationName,
  pathExistsInEntries,
  remapChildPathForRename,
} from './browserTreeOperations.js';

test('normalizeTreeOperationName accepts one path segment', () => {
  assert.equal(normalizeTreeOperationName(' README.md '), 'README.md');
  assert.throws(() => normalizeTreeOperationName(''), /Name is required/);
  assert.throws(() => normalizeTreeOperationName('../secrets'), /single file or folder name/);
  assert.throws(() => normalizeTreeOperationName('nested/file.txt'), /single file or folder name/);
});

test('tree operation path helpers join and remap paths', () => {
  assert.equal(joinTreePath('', 'README.md'), 'README.md');
  assert.equal(joinTreePath('docs', 'README.md'), 'docs/README.md');
  assert.equal(getDirectoryMarkerPath('docs/new'), 'docs/new/.gitslicekeep');
  assert.equal(
    remapChildPathForRename('docs/old', 'docs/new', 'docs/old/src/app.js'),
    'docs/new/src/app.js',
  );
  assert.equal(remapChildPathForRename('docs/old', 'docs/new', 'unrelated/app.js'), 'unrelated/app.js');
});

test('filterVisibleTreeEntries hides folder marker files only', () => {
  const entries = [
    { path: 'docs/.gitslicekeep', type: 'file' },
    { path: 'docs/README.md', type: 'file' },
    { path: 'docs/src', type: 'directory' },
  ];
  assert.deepEqual(filterVisibleTreeEntries(entries).map((entry) => entry.path), [
    'docs/README.md',
    'docs/src',
  ]);
});

test('pathExistsInEntries compares normalized entry paths', () => {
  const entries = [{ path: '/docs/README.md' }, { path: 'docs/src' }];
  assert.equal(pathExistsInEntries(entries, 'docs/README.md'), true);
  assert.equal(pathExistsInEntries(entries, 'docs/missing.md'), false);
});

test('changeset and merge helpers normalize gateway payloads', () => {
  assert.equal(normalizeChangesetId({ changesetId: 'cs_123' }), 'cs_123');
  assert.equal(normalizeChangesetId({ changeset_id: 'cs_456' }), 'cs_456');
  assert.equal(isSuccessfulMergeResponse({ status: 0 }), true);
  assert.equal(isSuccessfulMergeResponse({ status: 'MERGE_STATUS_SUCCESS' }), true);
  assert.equal(isSuccessfulMergeResponse({ status: 'MERGE_STATUS_STALE_BASE' }), false);
});

test('encodeTreeTextContent encodes UTF-8 content as base64', () => {
  assert.equal(encodeTreeTextContent('I\u2019m here'), 'SeKAmW0gaGVyZQ==');
});

test('tree create scope blocks home slice root creates', () => {
  assert.equal(
    getTreeCreateBlockedReason({
      sliceId: 'home_nicholas',
      currentSlice: { slice_id: 'home_nicholas' },
      parentPath: '',
    }),
    'Open a folder in the home slice before creating files or folders.',
  );
  assert.equal(
    getTreeCreateBlockedReason({
      sliceId: 'home_nicholas',
      currentSlice: { slice_id: 'home_nicholas' },
      parentPath: 'nicholas/workspace',
    }),
    '',
  );
});

test('tree create scope restricts custom slices to tracked folders', () => {
  const currentSlice = {
    slice_id: 'slice_docs',
    folder_mounts: [{ source_path: 'nicholas/docs', alias: 'docs' }],
  };
  assert.deepEqual(getSliceWriteRoots({ sliceId: 'slice_docs', currentSlice }), ['docs', 'nicholas/docs']);
  assert.equal(
    getTreeCreateBlockedReason({ sliceId: 'slice_docs', currentSlice, parentPath: '' }),
    'Open a tracked folder before creating files or folders in this slice.',
  );
  assert.equal(
    getTreeCreateBlockedReason({ sliceId: 'slice_docs', currentSlice, parentPath: 'nicholas' }),
    'Create files and folders inside one of this slice\'s tracked folders.',
  );
  assert.equal(
    getTreeCreateBlockedReason({ sliceId: 'slice_docs', currentSlice, parentPath: 'nicholas/docs' }),
    '',
  );
  assert.equal(
    getTreeCreateBlockedReason({ sliceId: 'slice_docs', currentSlice, parentPath: 'docs' }),
    '',
  );
});
