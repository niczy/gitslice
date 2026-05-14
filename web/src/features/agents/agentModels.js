import {
  CLOUD_ONLY_CONVERSATION_AVAILABILITY,
  LOCAL_CONVERSATION_AVAILABILITY,
  PENDING_LOCAL_CONVERSATION_AVAILABILITY,
} from './agentConstants.js';

export function normalizeConversationAvailability(value) {
  const normalized = String(value || '').trim().toLowerCase();
  return normalized || 'unknown';
}

export function normalizeConversationState(state) {
  const normalized = String(state || '').trim().toLowerCase();
  if (normalized === 'stopping' || normalized === 'stopped') {
    return 'idle';
  }
  return normalized;
}

export function normalizeSession(session) {
  return {
    sessionId: session?.sessionId ?? session?.session_id ?? '',
    sliceId: session?.sliceId ?? session?.slice_id ?? '',
    state: normalizeConversationState(session?.state),
    availability: normalizeConversationAvailability(session?.availability ?? session?.local_availability),
    createdAt: session?.createdAt ?? session?.created_at ?? '',
    lastActivityAt: session?.lastActivityAt ?? session?.last_activity_at ?? '',
    environment: session?.environment ?? '',
    agentType: session?.agentType ?? session?.agent_type ?? '',
    provider: session?.provider ?? '',
    runnerId: session?.runnerId ?? session?.runner_id ?? '',
  };
}

export function normalizeRunner(runner) {
  return {
    runnerId: runner?.runnerId ?? runner?.runner_id ?? '',
    provider: runner?.provider ?? '',
    agentType: runner?.agentType ?? runner?.agent_type ?? '',
    status: runner?.status ?? '',
    hostName: runner?.hostName ?? runner?.host_name ?? '',
    pid: runner?.pid ?? 0,
    workspaceRoot: runner?.workspaceRoot ?? runner?.workspace_root ?? '',
    version: runner?.version ?? '',
    lastHeartbeatAt: runner?.lastHeartbeatAt ?? runner?.last_heartbeat_at ?? '',
  };
}

export function runnerDisplayName(runner) {
  const workspaceRoot = String(runner?.workspaceRoot || '').trim();
  if (workspaceRoot) {
    const parts = workspaceRoot.split('/').filter(Boolean);
    return parts[parts.length - 1] || workspaceRoot;
  }
  if (runner?.hostName) {
    return runner.hostName;
  }
  if (runner?.runnerId) {
    return runner.runnerId.slice(0, 12);
  }
  return runner?.agentType || 'agent';
}

export function shortSessionId(sessionId) {
  return sessionId ? sessionId.replace(/^sess_?/, '').slice(0, 12) : 'unknown';
}

export function shortEntityId(value, length = 12) {
  const text = String(value || '').trim();
  return text ? text.slice(0, length) : '';
}

export function agentDisplayName(agentType) {
  switch (String(agentType || '').trim().toLowerCase()) {
    case 'codex':
      return 'Codex';
    case 'claude':
      return 'Claude';
    default:
      return 'Agent';
  }
}

export function formatAgentTimestamp(value) {
  if (!value) {
    return 'Unknown date';
  }
  const numeric = Number(value);
  const date = Number.isFinite(numeric) && numeric > 0
    ? new Date(numeric * 1000)
    : new Date(value);
  if (Number.isNaN(date.getTime())) {
    return 'Unknown date';
  }
  return date.toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

export function runnerStateValue(runnerState, snakeKey, camelKey) {
  return infoValue(runnerState?.[snakeKey] || runnerState?.[camelKey]);
}

export function infoValue(value) {
  if (Array.isArray(value)) {
    return value.join(' ');
  }
  if (value === null || value === undefined) {
    return '';
  }
  return String(value).trim();
}

export function buildRunningAgentInfoRows(runner, session, runnerState) {
  if (!runner) {
    return [];
  }
  const rows = [
    ['Status', runner.status],
    ['Runner', runner.runnerId],
    ['Provider', runner.provider || session?.provider || 'local'],
    ['CLI', runner.agentType || session?.agentType],
    ['Host', runnerState.host_name || runnerState.hostName || runner.hostName],
    ['PID', runnerState.pid || runner.pid],
    ['Workspace', runner.workspaceRoot || runnerState.workspace_root || runnerState.workspaceRoot],
    ['Running directory', runnerState.running_dir || runnerState.runningDir || runnerState.checkout_dir || runnerState.checkoutDir],
    ['Command', runnerState.command],
    ['Codex mode', runnerState.codex_mode || runnerState.codexMode],
    ['Attached', runnerState.attached_at || runnerState.attachedAt],
    ['Selected conversation', session?.runnerId === runner.runnerId ? session.sessionId : ''],
    ['Last heartbeat', runner.lastHeartbeatAt],
  ];
  return rows
    .map(([label, value]) => [label, infoValue(value)])
    .filter(([, value]) => value);
}

export function isConversationLocal(session) {
  return session?.availability === LOCAL_CONVERSATION_AVAILABILITY || isConversationPendingLocal(session);
}

export function isConversationCloudOnly(session) {
  return session?.availability === CLOUD_ONLY_CONVERSATION_AVAILABILITY;
}

export function isConversationPendingLocal(session) {
  if (session?.availability === PENDING_LOCAL_CONVERSATION_AVAILABILITY) {
    return true;
  }
  return session?.availability === 'unknown'
    && String(session?.provider || '').trim().toLowerCase() === 'local'
    && Boolean(String(session?.runnerId || '').trim());
}

export function conversationAvailabilityLabel(session) {
  if (isConversationLocal(session)) {
    return 'Local';
  }
  switch (session?.availability) {
    case CLOUD_ONLY_CONVERSATION_AVAILABILITY:
      return 'Cloud only';
    case 'runner_offline':
      return 'Runner offline';
    case 'failed':
      return 'Failed';
    default:
      return 'Local status unavailable';
  }
}

export function conversationAvailabilityMessage(session) {
  if (isConversationPendingLocal(session)) {
    return 'Waiting for this runner to create the local checkout for the conversation.';
  }
  switch (session?.availability) {
    case CLOUD_ONLY_CONVERSATION_AVAILABILITY:
      return 'This conversation exists on the server, but this runner does not have a local copy yet.';
    case 'runner_offline':
      return 'The runner for this conversation is offline.';
    case 'failed':
      return 'This conversation failed before it became available locally.';
    default:
      return 'The server cannot confirm a local copy for this conversation.';
  }
}

export function conversationAvailabilityRank(session) {
  if (isConversationPendingLocal(session)) {
    return 1;
  }
  switch (session?.availability) {
    case LOCAL_CONVERSATION_AVAILABILITY:
      return 0;
    case CLOUD_ONLY_CONVERSATION_AVAILABILITY:
      return 2;
    default:
      return 3;
  }
}
