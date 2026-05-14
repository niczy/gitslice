import assert from 'node:assert/strict';
import test from 'node:test';

import {
  buildConversationItems,
  buildLiveStreamState,
  eventBody,
  eventTitle,
  eventTone,
  normalizeEvent,
  renderConversationMarkdown,
} from './agentEvents.js';

test('normalizeEvent decodes event payloads', () => {
  const event = normalizeEvent({
    seq: '3',
    ts: '2026-05-14T00:00:00Z',
    stream: 'agent',
    type: 'output_final',
    payload: '{"text":"done"}',
  });

  assert.deepEqual(event, {
    seq: 3,
    ts: '2026-05-14T00:00:00Z',
    stream: 'agent',
    type: 'output_final',
    kind: '',
    payload: { text: 'done' },
  });
});

test('buildConversationItems folds thinking tokens into one item', () => {
  const items = buildConversationItems([
    { seq: 1, ts: 't1', stream: 'agent', type: 'input', payload: { text: 'make a change' } },
    { seq: 2, ts: 't2', stream: 'agent', type: 'thinking_delta', payload: { text: 'Checking ' } },
    { seq: 3, ts: 't3', stream: 'agent', type: 'thinking_delta', payload: { text: 'files.' } },
    { seq: 4, ts: 't4', stream: 'agent', type: 'output_final', payload: { text: 'Done', exitCode: 0 } },
  ], { active: false }, { agentType: 'codex' });

  assert.equal(items.length, 3);
  assert.equal(items[0].kind, 'message');
  assert.equal(items[1].kind, 'thinking');
  assert.equal(items[1].text, 'Checking files.');
  assert.equal(items[2].kind, 'message');
  assert.equal(items[2].message.label, 'Codex');
});

test('buildLiveStreamState exposes pending thinking and response text', () => {
  const state = buildLiveStreamState([
    { seq: 1, stream: 'agent', type: 'input', payload: { text: 'continue' } },
    { seq: 2, stream: 'agent', type: 'reasoning_delta', payload: { text: 'Thinking.' } },
    { seq: 3, stream: 'agent', type: 'output_delta', payload: { text: 'Working' } },
  ], {
    state: 'idle',
    availability: 'local',
  });

  assert.deepEqual(state, {
    active: true,
    pendingInputSeq: 1,
    thinkingText: 'Thinking.',
    responseText: 'Working',
  });
});

test('event display helpers preserve local changes and control errors', () => {
  const localChangesEvent = {
    stream: 'status',
    type: 'local_changes',
    payload: {
      changes: { added: 1 },
      path_count: 1,
      paths: [{ path: 'a.txt', status: 'A' }],
    },
  };
  const warningEvent = {
    stream: 'control',
    type: 'error',
    payload: {
      code: 'CODEX_CONFIG_WARNING',
      message: 'missing optional config',
    },
  };

  assert.equal(eventTitle(localChangesEvent), 'Local changes');
  assert.equal(eventBody(localChangesEvent), '+1');
  assert.equal(eventTone(warningEvent), 'status');
});

test('renderConversationMarkdown returns a paragraph fallback', () => {
  assert.equal(renderConversationMarkdown(''), '<p></p>');
  assert.equal(renderConversationMarkdown('hello'), '<p>hello</p>');
});
