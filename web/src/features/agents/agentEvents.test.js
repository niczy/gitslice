import assert from 'node:assert/strict';
import test from 'node:test';

import {
  buildConversationItems,
  buildLiveStreamState,
  eventBody,
  eventTitle,
  eventTone,
  latestCheckoutFailureEvent,
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

test('buildConversationItems preserves interleaved thinking and tool events', () => {
  const items = buildConversationItems([
    { seq: 1, ts: 't1', stream: 'agent', type: 'input', payload: { text: 'make a change' } },
    { seq: 2, ts: 't2', stream: 'agent', type: 'thinking_delta', payload: { text: 'Checking files. ' } },
    { seq: 3, ts: 't3', stream: 'tool', type: 'start', payload: { tool: 'Bash', input: { command: 'npm test' } } },
    { seq: 4, ts: 't4', stream: 'agent', type: 'thinking_delta', payload: { text: 'Inspecting output.' } },
    { seq: 5, ts: 't5', stream: 'tool', type: 'output', payload: { eventType: 'command_exec_output', text: 'ok' } },
    { seq: 6, ts: 't6', stream: 'agent', type: 'output_final', payload: { text: 'Done', exitCode: 0 } },
  ], { active: false }, { agentType: 'codex' });

  assert.deepEqual(items.map((item) => item.kind), ['message', 'thinking', 'events', 'thinking', 'events', 'message']);
  assert.equal(items[1].text, 'Checking files. ');
  assert.equal(items[2].events[0].seq, 3);
  assert.equal(items[3].text, 'Inspecting output.');
  assert.equal(items[4].events[0].seq, 5);
});

test('buildConversationItems renders live deltas inline without duplicating aggregate stream state', () => {
  const items = buildConversationItems([
    { seq: 1, ts: 't1', stream: 'agent', type: 'input', payload: { text: 'continue' } },
    { seq: 2, ts: 't2', stream: 'agent', type: 'thinking_delta', payload: { text: 'Thinking.' } },
  ], {
    active: true,
    pendingInputSeq: 1,
    thinkingText: 'Thinking.',
    responseText: '',
  }, { agentType: 'codex' });

  assert.deepEqual(items.map((item) => item.kind), ['message', 'thinking']);
  assert.equal(items[1].text, 'Thinking.');
  assert.equal(items[1].live, true);
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

test('tool event display helpers summarize known tool payloads', () => {
  const bashEvent = {
    stream: 'tool',
    type: 'start',
    payload: {
      tool: 'Bash',
      input: {
        command: 'npm test',
        description: 'run tests',
      },
    },
  };
  const readEvent = {
    stream: 'tool',
    type: 'start',
    payload: {
      tool: 'Read',
      input: {
        file_path: 'src/app.js',
      },
    },
  };
  const outputEvent = {
    stream: 'tool',
    type: 'output',
    payload: {
      eventType: 'command_exec_output',
      text: 'ok',
    },
  };

  assert.equal(eventTitle(bashEvent), 'Run command');
  assert.equal(eventBody(bashEvent), 'npm test\nDescription: run tests');
  assert.equal(eventTitle(readEvent), 'Read file');
  assert.equal(eventBody(readEvent), 'Path: src/app.js');
  assert.equal(eventTitle(outputEvent), 'Command output');
  assert.equal(eventBody(outputEvent), 'ok');
});

test('latestCheckoutFailureEvent returns unresolved terminal checkout failures', () => {
  const retryEvent = {
    seq: 1,
    stream: 'control',
    type: 'warning',
    payload: {
      code: 'LOCAL_AGENT_CHECKOUT_RETRYING',
      message: 'retrying',
    },
  };
  const failureEvent = {
    seq: 2,
    stream: 'control',
    type: 'error',
    payload: {
      code: 'LOCAL_AGENT_CHECKOUT_FAILED',
      message: 'Checkout failed after 3 attempts',
    },
  };

  assert.equal(latestCheckoutFailureEvent([retryEvent, failureEvent]), failureEvent);
  assert.equal(latestCheckoutFailureEvent([
    retryEvent,
    failureEvent,
    { seq: 3, stream: 'status', type: 'local_runner_attached', payload: {} },
  ]), null);
});

test('renderConversationMarkdown returns a paragraph fallback', () => {
  assert.equal(renderConversationMarkdown(''), '<p></p>');
  assert.equal(renderConversationMarkdown('hello'), '<p>hello</p>');
});
