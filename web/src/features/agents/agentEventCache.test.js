import assert from 'node:assert/strict';
import test from 'node:test';

import {
  mergeCachedAgentEvents,
  normalizeCachedAgentEvents,
} from './agentEventCache.js';

test('normalizeCachedAgentEvents sorts, deduplicates, and trims events', () => {
  const events = normalizeCachedAgentEvents([
    { seq: 3, stream: 'agent', type: 'output_delta', payload: { text: 'c' } },
    { seq: 1, stream: 'agent', type: 'input', payload: { text: 'a' } },
    { seq: 2, stream: 'agent', type: 'thinking_delta', payload: { text: 'b' } },
    { seq: 3, stream: 'agent', type: 'output_final', payload: { text: 'done' } },
    { seq: 0, stream: 'agent', type: 'ignored', payload: {} },
  ], 2);

  assert.deepEqual(events.map((event) => [event.seq, event.type]), [
    [2, 'thinking_delta'],
    [3, 'output_final'],
  ]);
});

test('mergeCachedAgentEvents appends diffs without duplicating existing seqs', () => {
  const events = mergeCachedAgentEvents([
    { seq: 1, stream: 'agent', type: 'input', payload: { text: 'first' } },
    { seq: 2, stream: 'agent', type: 'output_delta', payload: { text: 'old' } },
  ], [
    { seq: 2, stream: 'agent', type: 'output_delta', payload: { text: 'new' } },
    { seq: 3, stream: 'agent', type: 'output_final', payload: { text: 'done' } },
  ]);

  assert.deepEqual(events.map((event) => [event.seq, event.payload.text]), [
    [1, 'first'],
    [2, 'new'],
    [3, 'done'],
  ]);
});
