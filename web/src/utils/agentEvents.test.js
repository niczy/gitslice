import assert from 'node:assert/strict';
import test from 'node:test';

import { bytesToBase64, utf8ToBytes } from '../../shared/runtime.js';
import { parseAgentEventPayload } from './agentEvents.js';

test('parseAgentEventPayload decodes base64 JSON as UTF-8', () => {
  const encoded = bytesToBase64(utf8ToBytes(JSON.stringify({ text: 'I’m checking the session.' })));

  assert.deepEqual(parseAgentEventPayload(encoded), {
    text: 'I’m checking the session.',
  });
});

test('parseAgentEventPayload accepts raw JSON strings', () => {
  assert.deepEqual(parseAgentEventPayload('{"text":"hello"}'), {
    text: 'hello',
  });
});
