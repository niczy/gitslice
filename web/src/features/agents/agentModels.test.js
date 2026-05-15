import assert from 'node:assert/strict';
import test from 'node:test';

import {
  conversationAvailabilityLabel,
  conversationAvailabilityRank,
  isConversationLocal,
  normalizeRunner,
  normalizeSession,
  runnerDisplayName,
} from './agentModels.js';

test('normalizeSession accepts snake_case payloads and maps legacy stopped state', () => {
  const session = normalizeSession({
    session_id: 'sess_123',
    slice_id: 'slice_abc',
    state: 'stopped',
    local_availability: 'pending_local',
    created_at: '2026-05-14T00:00:00Z',
    last_activity_at: '2026-05-14T00:01:00Z',
    agent_type: 'codex',
    runner_id: 'agr_123',
    provider: 'local',
  });

  assert.deepEqual(session, {
    sessionId: 'sess_123',
    sliceId: 'slice_abc',
    state: 'idle',
    availability: 'pending_local',
    createdAt: '2026-05-14T00:00:00Z',
    lastActivityAt: '2026-05-14T00:01:00Z',
    environment: '',
    agentType: 'codex',
    provider: 'local',
    runnerId: 'agr_123',
  });
  assert.equal(isConversationLocal(session), true);
  assert.equal(conversationAvailabilityLabel(session), 'Local');
});

test('conversationAvailabilityRank keeps local conversations first', () => {
  assert.equal(conversationAvailabilityRank({ availability: 'local' }), 0);
  assert.equal(conversationAvailabilityRank({ availability: 'pending_local' }), 1);
  assert.equal(conversationAvailabilityRank({ availability: 'cloud_only' }), 2);
  assert.equal(conversationAvailabilityRank({ availability: 'runner_offline' }), 3);
});

test('runnerDisplayName prefers workspace basename', () => {
  const runner = normalizeRunner({
    runner_id: 'agr_1234567890abcdef',
    host_name: 'build-host',
    workspace_root: '/tmp/workspaces/example-slice',
    agent_type: 'codex',
    capabilities: Buffer.from(JSON.stringify({
      default_agent_type: 'codex',
      supported_agent_types: ['codex', 'claude'],
    })).toString('base64'),
  });

  assert.equal(runnerDisplayName(runner), 'example-slice');
  assert.deepEqual(runner.supportedAgentTypes, ['codex', 'claude']);
  assert.equal(runner.defaultAgentType, 'codex');
});
