import assert from 'node:assert/strict';
import test from 'node:test';

import {
  changeStatusLabel,
  localChangeStateText,
  localChangesSummaryText,
  normalizeLocalChangesPayload,
} from './agentLocalChanges.js';

test('normalizeLocalChangesPayload accepts mixed casing and filters empty paths', () => {
  const localChanges = normalizeLocalChangesPayload({
    request_id: 'local_changes_123',
    working_tree: '/tmp/session',
    checkout_base: 'hash_abc',
    tracked_changeset_id: 'cs_123',
    path_count: 3,
    paths: [
      { path: 'added.txt', status: 'a', patch: '--- /dev/null\n+++ b/added.txt\n@@ -0,0 +1 @@\n+new', lines_added: 1 },
      { path: '', status: 'm' },
      { path: 'modified.txt', status: 'M', linesDeleted: 2, metadata_notes: ['mode: executable'] },
    ],
    diffs_included: true,
    changes: {
      added: 1,
      modified: 2,
    },
    truncated: true,
  });

  assert.equal(localChanges.requestId, 'local_changes_123');
  assert.equal(localChanges.workingTree, '/tmp/session');
  assert.equal(localChanges.checkoutBase, 'hash_abc');
  assert.equal(localChanges.trackedChangesetId, 'cs_123');
  assert.equal(localChanges.diffsIncluded, true);
  assert.equal(localChanges.pathCount, 3);
  assert.deepEqual(localChanges.paths, [
    {
      path: 'added.txt',
      status: 'A',
      patch: '--- /dev/null\n+++ b/added.txt\n@@ -0,0 +1 @@\n+new',
      linesAdded: 1,
      linesDeleted: 0,
      binary: false,
      metadataNotes: [],
    },
    {
      path: 'modified.txt',
      status: 'M',
      patch: '',
      linesAdded: 0,
      linesDeleted: 2,
      binary: false,
      metadataNotes: ['mode: executable'],
    },
  ]);
  assert.equal(localChanges.truncated, true);
  assert.deepEqual(localChanges.changes, {
    added: 1,
    modified: 2,
    deleted: 0,
  });
});

test('localChangesSummaryText summarizes clean and dirty trees', () => {
  assert.equal(localChangesSummaryText(null), 'Not loaded');
  assert.equal(localChangesSummaryText({ pathCount: 0, changes: { added: 0, modified: 0, deleted: 0 } }), 'Clean');
  assert.equal(localChangesSummaryText({ pathCount: 4, changes: { added: 1, modified: 2, deleted: 1 } }), '+1 ~2 -1');
  assert.equal(localChangesSummaryText({ pathCount: 4, changes: { added: 0, modified: 0, deleted: 0 } }), '4 changed');
});

test('changeStatusLabel maps common git status letters', () => {
  assert.equal(changeStatusLabel('A'), 'Added');
  assert.equal(changeStatusLabel('M'), 'Modified');
  assert.equal(changeStatusLabel('D'), 'Deleted');
  assert.equal(changeStatusLabel('?'), '?');
});

test('localChangeStateText includes diff and metadata state', () => {
  assert.equal(localChangeStateText({ status: 'A', linesAdded: 2, linesDeleted: 0 }), 'Added +2');
  assert.equal(localChangeStateText({ status: 'M', linesAdded: 1, linesDeleted: 1 }), 'Modified +1 -1');
  assert.equal(localChangeStateText({ status: 'D', binary: true }), 'Deleted binary');
  assert.equal(localChangeStateText({ status: 'M', metadataNotes: ['mode: executable'] }), 'Modified metadata');
});
