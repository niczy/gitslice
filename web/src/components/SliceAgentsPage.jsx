import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import {
  Bot,
  CircleAlert,
  CloudOff,
  ExternalLink,
  FileDiff,
  GitPullRequest,
  Info,
  PanelLeftClose,
  PanelLeftOpen,
  PanelRightClose,
  PanelRightOpen,
  Plus,
  RefreshCw,
  Send,
  TerminalSquare,
  X,
} from 'lucide-react';

import {
  createAgentSession,
  getAgentCapabilities,
  listAgentRunners,
  listAgentSessionEvents,
  listAgentSessions,
  requestAgentSessionChangesetExport,
  requestAgentSessionLocalChanges,
  requestAgentRunnerRestart,
  sendAgentSessionInput,
} from '../utils/api.js';
import { parseAgentEventPayload } from '../utils/agentEvents.js';
import { renderMarkdownHtml } from '../utils/markdown.js';
import { getSliceDisplayName } from '../utils/slices.js';
import SliceDetailNav from './SliceDetailNav.jsx';
import { Button } from './ui/button.jsx';

function normalizeSession(session) {
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

function normalizeConversationAvailability(value) {
  const normalized = String(value || '').trim().toLowerCase();
  return normalized || 'unknown';
}

function normalizeConversationState(state) {
  const normalized = String(state || '').trim().toLowerCase();
  if (normalized === 'stopping' || normalized === 'stopped') {
    return 'idle';
  }
  return normalized;
}

function normalizeRunner(runner) {
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

function runnerDisplayName(runner) {
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

function normalizeEvent(event) {
  return {
    seq: Number(event?.seq || 0),
    ts: event?.ts || '',
    stream: event?.stream || '',
    type: event?.type || '',
    kind: event?.kind || '',
    payload: parseAgentEventPayload(event?.payload),
  };
}

function shortSessionId(sessionId) {
  return sessionId ? sessionId.replace(/^sess_?/, '').slice(0, 12) : 'unknown';
}

function shortEntityId(value, length = 12) {
  const text = String(value || '').trim();
  return text ? text.slice(0, length) : '';
}

function agentDisplayName(agentType) {
  switch (String(agentType || '').trim().toLowerCase()) {
    case 'codex':
      return 'Codex';
    case 'claude':
      return 'Claude';
    default:
      return 'Agent';
  }
}

function formatAgentTimestamp(value) {
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

function eventTitle(event) {
  if (event.stream === 'status' && event.type === 'state') {
    return `State ${normalizeConversationState(event.payload?.state) || 'changed'}`;
  }
  if (event.stream === 'status' && event.type === 'local_runner_attached') {
    return 'Runner attached';
  }
  if (event.stream === 'status' && event.type === 'local_changes') {
    return 'Local changes';
  }
  if (event.stream === 'tool') {
    return event.payload?.tool || event.payload?.id || 'Tool';
  }
  if (event.stream === 'control') {
    switch (event.type) {
      case 'local_changes_requested':
        return 'Local changes requested';
      case 'local_changes_failed':
        return 'Local changes failed';
      case 'changeset_export_requested':
        return 'Changeset export requested';
      case 'changeset_export_started':
        return 'Changeset export started';
      case 'changeset_export_completed':
        return 'Changeset exported';
      case 'changeset_export_failed':
        return 'Changeset export failed';
      case 'local_runner_restart_requested':
        return 'Runner restart requested';
      case 'local_runner_restart_started':
        return 'Runner restart started';
      case 'local_runner_upgrade_completed':
        return 'Runner upgrade completed';
      case 'local_runner_restart_spawned':
        return 'Runner replacement started';
      case 'local_runner_restart_failed':
        return 'Runner restart failed';
      default:
        return event.payload?.code || 'Control';
    }
  }
  return event.stream || 'Agent';
}

function eventBody(event) {
  if (event.stream === 'status' && event.type === 'state') {
    return normalizeConversationState(event.payload?.state) || JSON.stringify(event.payload || {});
  }
  if (event.stream === 'status' && event.type === 'local_runner_attached') {
    const host = event.payload?.host_name || event.payload?.hostName || '';
    const dir = event.payload?.running_dir || event.payload?.runningDir || event.payload?.checkout_dir || event.payload?.checkoutDir || '';
    return [host, dir].filter(Boolean).join(' · ') || JSON.stringify(event.payload || {});
  }
  if (event.stream === 'status' && event.type === 'local_changes') {
    const localChanges = normalizeLocalChangesPayload(event.payload || {});
    return [
      localChangesSummaryText(localChanges),
      localChanges.trackedChangesetId ? `tracked ${localChanges.trackedChangesetId}` : '',
      localChanges.refreshedAt || '',
    ].filter(Boolean).join(' · ') || JSON.stringify(event.payload || {});
  }
  if (event.stream === 'control' && event.type?.startsWith('changeset_export_')) {
    return [
      event.payload?.status,
      event.payload?.changeset_id || event.payload?.changesetId,
      event.payload?.message,
    ].filter(Boolean).join(' · ') || JSON.stringify(event.payload || {});
  }
  if (event.stream === 'control' && event.type?.startsWith('local_changes_')) {
    return [
      event.payload?.status,
      event.payload?.message,
      event.payload?.request_id || event.payload?.requestId,
    ].filter(Boolean).join(' · ') || JSON.stringify(event.payload || {});
  }
  if (event.stream === 'control' && event.type?.startsWith('local_runner_')) {
    const replacementPID = event.payload?.replacement_pid || event.payload?.replacementPid || '';
    return [
      event.payload?.status,
      event.payload?.action,
      event.payload?.message,
      replacementPID ? `replacement pid ${replacementPID}` : '',
    ].filter(Boolean).join(' · ') || JSON.stringify(event.payload || {});
  }
  return event.payload?.text
    || event.payload?.message
    || event.payload?.status
    || event.payload?.state
    || event.payload?.tool
    || JSON.stringify(event.payload || {});
}

function eventTone(event) {
  const kind = eventKind(event);
  if (kind === 'error' || isTerminalControlError(event)) {
    return 'error';
  }
  if (kind === 'tool_call' || kind === 'tool_result' || event.stream === 'tool') {
    return 'tool';
  }
  if (event.stream === 'status') {
    return 'status';
  }
  if (event.stream === 'control') {
    return 'status';
  }
  return 'agent';
}

const BLOCKED_CONVERSATION_STATES = new Set(['failed']);
const LOCAL_CONVERSATION_AVAILABILITY = 'local';
const PENDING_LOCAL_CONVERSATION_AVAILABILITY = 'pending_local';
const CLOUD_ONLY_CONVERSATION_AVAILABILITY = 'cloud_only';
const AGENT_EVENTS_PAGE_SIZE = 500;
const AGENT_EVENTS_MAX = 5000;
const SESSIONS_SIDEBAR_MOBILE_MAX_WIDTH = 900;
const AGENTS_SIDEBAR_MIN_WIDTH = 280;
const AGENTS_SIDEBAR_MAX_WIDTH = 560;
const AGENTS_SIDEBAR_DEFAULT_WIDTH = 340;
const AGENTS_SIDEBAR_WIDTH_STORAGE_KEY = 'gitslice.agentsSidebarWidth';
const LOCAL_CHANGES_REQUEST_TIMEOUT_MS = 30000;
const NON_TERMINAL_CONTROL_ERROR_CODES = new Set(['CODEX_CONFIG_WARNING']);
const useIsomorphicLayoutEffect = typeof window === 'undefined' ? useEffect : useLayoutEffect;

function clampAgentsSidebarWidth(value, maxWidth = AGENTS_SIDEBAR_MAX_WIDTH) {
  const numeric = Number(value);
  if (!Number.isFinite(numeric)) {
    return AGENTS_SIDEBAR_DEFAULT_WIDTH;
  }
  return Math.min(Math.max(maxWidth, AGENTS_SIDEBAR_MIN_WIDTH), Math.max(AGENTS_SIDEBAR_MIN_WIDTH, numeric));
}

function readAgentsSidebarWidth() {
  if (typeof window === 'undefined') {
    return AGENTS_SIDEBAR_DEFAULT_WIDTH;
  }
  try {
    return clampAgentsSidebarWidth(window.localStorage.getItem(AGENTS_SIDEBAR_WIDTH_STORAGE_KEY));
  } catch {
    return AGENTS_SIDEBAR_DEFAULT_WIDTH;
  }
}

function writeAgentsSidebarWidth(value) {
  if (typeof window === 'undefined') {
    return;
  }
  try {
    window.localStorage.setItem(AGENTS_SIDEBAR_WIDTH_STORAGE_KEY, String(Math.round(value)));
  } catch {
    // Persisting panel width is a convenience only.
  }
}

function payloadText(payload) {
  return payload?.text
    || payload?.delta
    || payload?.message
    || payload?.status
    || payload?.state
    || payload?.tool
    || '';
}

function payloadExitCode(payload) {
  const raw = payload?.exitCode ?? payload?.exit_code;
  const numeric = Number(raw);
  return Number.isFinite(numeric) ? numeric : 0;
}

function payloadRequestId(payload) {
  return String(payload?.requestId || payload?.request_id || '').trim();
}

function latestEvent(events, predicate) {
  for (let i = events.length - 1; i >= 0; i -= 1) {
    if (predicate(events[i])) {
      return events[i];
    }
  }
  return null;
}

function latestEventSeq(events, predicate) {
  return latestEvent(events, predicate)?.seq || 0;
}

function controlErrorCode(event) {
  return String(event?.payload?.code || event?.payload?.errorCode || event?.payload?.error_code || '')
    .trim()
    .toUpperCase();
}

function isTerminalControlError(event) {
  if (event?.stream !== 'control' || event?.type !== 'error') {
    return false;
  }
  return !NON_TERMINAL_CONTROL_ERROR_CODES.has(controlErrorCode(event));
}

function renderConversationMarkdown(text) {
  return renderMarkdownHtml(text) || '<p></p>';
}

function conversationMessageFromEvent(event, agentLabel = 'Agent') {
  if (event.stream === 'agent' && event.type === 'input') {
    return {
      key: `${event.seq}-user`,
      role: 'user',
      label: 'You',
      ts: event.ts,
      text: payloadText(event.payload),
      failed: false,
    };
  }
  if (event.stream === 'agent' && event.type === 'output_final') {
    const exitCode = payloadExitCode(event.payload);
    return {
      key: `${event.seq}-assistant`,
      role: exitCode === 0 ? 'assistant' : 'error',
      label: exitCode === 0 ? agentLabel : `${agentLabel} error`,
      ts: event.ts,
      text: payloadText(event.payload),
      failed: exitCode !== 0,
    };
  }
  return null;
}

function eventKind(event) {
  if (event?.stream === 'control' && event?.type === 'error' && !isTerminalControlError(event)) {
    return 'control';
  }
  const explicit = String(event?.kind || '').trim().toLowerCase();
  if (explicit) {
    return explicit;
  }
  const stream = String(event?.stream || '').trim().toLowerCase();
  const type = String(event?.type || '').trim().toLowerCase();
  if (stream === 'agent' && type === 'input') return 'user_input';
  if (stream === 'agent' && ['thinking_delta', 'reasoning_delta', 'reasoning_summary_delta'].includes(type)) return 'thinking';
  if (stream === 'agent' && ['output_delta', 'output_final'].includes(type)) return 'model_response';
  if (stream === 'tool' && ['start', 'call', 'request'].includes(type)) return 'tool_call';
  if (stream === 'tool' && ['output', 'result', 'end'].includes(type)) return 'tool_result';
  if (stream === 'status') return 'status';
  if (stream === 'control' && type === 'error') return isTerminalControlError(event) ? 'error' : 'control';
  if (stream === 'control') return 'control';
  return 'event';
}

function isThinkingEvent(event) {
  return eventKind(event) === 'thinking';
}

function isModelResponseDelta(event) {
  return eventKind(event) === 'model_response' && event.type === 'output_delta';
}

function normalizeLocalChangePath(entry) {
  if (!entry) {
    return null;
  }
  const path = String(entry.path || '').trim();
  if (!path) {
    return null;
  }
  return {
    path,
    status: String(entry.status || '').trim().toUpperCase() || '?',
  };
}

function normalizeLocalChangesPayload(payload = {}) {
  const rawChanges = payload?.changes || {};
  const paths = Array.isArray(payload?.paths)
    ? payload.paths.map(normalizeLocalChangePath).filter(Boolean)
    : [];
  const pathCount = Number(payload?.pathCount ?? payload?.path_count ?? paths.length);
  return {
    requestId: payloadRequestId(payload),
    status: String(payload?.status || '').trim(),
    workingTree: String(payload?.workingTree || payload?.working_tree || '').trim(),
    checkoutBase: String(payload?.checkoutBase || payload?.checkout_base || '').trim(),
    trackedChangesetId: String(payload?.trackedChangesetId || payload?.tracked_changeset_id || '').trim(),
    pathCount: Number.isFinite(pathCount) ? pathCount : paths.length,
    paths,
    truncated: Boolean(payload?.truncated),
    refreshedAt: payload?.refreshedAt || payload?.refreshed_at || '',
    changes: {
      added: Number(rawChanges.added || 0),
      modified: Number(rawChanges.modified || 0),
      deleted: Number(rawChanges.deleted || 0),
    },
  };
}

function localChangesSummaryText(localChanges) {
  if (!localChanges) {
    return 'Not loaded';
  }
  if (localChanges.pathCount === 0) {
    return 'Clean';
  }
  const parts = [
    localChanges.changes.added ? `+${localChanges.changes.added}` : '',
    localChanges.changes.modified ? `~${localChanges.changes.modified}` : '',
    localChanges.changes.deleted ? `-${localChanges.changes.deleted}` : '',
  ].filter(Boolean);
  return parts.length ? parts.join(' ') : `${localChanges.pathCount} changed`;
}

function changeStatusLabel(status) {
  switch (String(status || '').toUpperCase()) {
    case 'A':
      return 'Added';
    case 'M':
      return 'Modified';
    case 'D':
      return 'Deleted';
    default:
      return status || 'Changed';
  }
}

function latestLocalChangesEvent(events) {
  return latestEvent(events, (event) => event.stream === 'status' && event.type === 'local_changes');
}

function latestLocalChangesFailureEvent(events) {
  return latestEvent(events, (event) => event.stream === 'control' && event.type === 'local_changes_failed');
}

function latestChangesetExportEvent(events) {
  return latestEvent(events, (event) => event.stream === 'control' && event.type === 'changeset_export_completed');
}

function latestChangesetExportFailureEvent(events) {
  return latestEvent(events, (event) => event.stream === 'control' && event.type === 'changeset_export_failed');
}

function buildLiveStreamState(events, session) {
  if (!session || !isConversationLocal(session) || BLOCKED_CONVERSATION_STATES.has(session.state) || session.availability === 'failed') {
    return {
      active: false,
      pendingInputSeq: 0,
      thinkingText: '',
      responseText: '',
    };
  }

  let pendingInputSeq = 0;
  let thinkingText = '';
  let responseText = '';
  for (const event of events) {
    if (event.stream === 'agent' && event.type === 'input' && payloadText(event.payload).trim()) {
      pendingInputSeq = event.seq;
      thinkingText = '';
      responseText = '';
    } else if (event.stream === 'agent' && event.type === 'output_final') {
      pendingInputSeq = 0;
      thinkingText = '';
      responseText = '';
    } else if (isTerminalControlError(event)) {
      pendingInputSeq = 0;
      thinkingText = '';
      responseText = '';
    } else if (event.stream === 'status' && event.type === 'state' && BLOCKED_CONVERSATION_STATES.has(normalizeConversationState(event.payload?.state))) {
      pendingInputSeq = 0;
      thinkingText = '';
      responseText = '';
    } else if (pendingInputSeq > 0 && event.seq > pendingInputSeq && isThinkingEvent(event)) {
      thinkingText += payloadText(event.payload);
    } else if (pendingInputSeq > 0 && event.seq > pendingInputSeq && isModelResponseDelta(event)) {
      responseText += payloadText(event.payload);
    }
  }

  return {
    active: pendingInputSeq > 0,
    pendingInputSeq,
    thinkingText,
    responseText,
  };
}

function buildConversationItems(events, liveStreamState = { active: false }, session = null) {
  const items = [];
  let folded = [];
  let thinkingItem = null;
  let responseDraftItem = null;
  const pendingInputSeq = liveStreamState?.pendingInputSeq || 0;
  const agentLabel = agentDisplayName(session?.agentType);

  const flushFolded = () => {
    if (folded.length === 0) {
      return;
    }
    items.push({
      kind: 'events',
      key: `events-${folded[0].seq}-${folded[folded.length - 1].seq}`,
      events: folded,
    });
    folded = [];
  };

  const appendThinking = (event) => {
    const text = payloadText(event.payload);
    if (!text) {
      return;
    }
    flushFolded();
    if (!thinkingItem) {
      thinkingItem = {
        kind: 'thinking',
        key: `thinking-${event.seq}`,
        text: '',
        ts: event.ts,
        live: false,
      };
      items.push(thinkingItem);
    }
    thinkingItem.text += text;
  };

  const appendResponseDelta = (event) => {
    const text = payloadText(event.payload);
    if (!text) {
      return;
    }
    flushFolded();
    if (!responseDraftItem) {
      responseDraftItem = {
        kind: 'response-draft',
        key: `response-${event.seq}`,
        label: agentLabel,
        text: '',
        ts: event.ts,
        live: false,
      };
      items.push(responseDraftItem);
    }
    responseDraftItem.text += text;
  };

  const removeResponseDraft = () => {
    if (!responseDraftItem) {
      return;
    }
    const index = items.indexOf(responseDraftItem);
    if (index >= 0) {
      items.splice(index, 1);
    }
    responseDraftItem = null;
  };

  for (const event of events) {
    if (pendingInputSeq > 0 && event.seq > pendingInputSeq && (isThinkingEvent(event) || isModelResponseDelta(event))) {
      continue;
    }
    if (isThinkingEvent(event)) {
      appendThinking(event);
      continue;
    }
    if (isModelResponseDelta(event)) {
      appendResponseDelta(event);
      continue;
    }
    const message = conversationMessageFromEvent(event, agentLabel);
    if (message?.text) {
      if (message.role === 'user') {
        thinkingItem = null;
        responseDraftItem = null;
      } else {
        removeResponseDraft();
      }
      flushFolded();
      items.push({
        kind: 'message',
        key: message.key,
        message,
      });
      if (message.role !== 'user') {
        thinkingItem = null;
        responseDraftItem = null;
      }
    } else if (event.stream === 'agent' && event.type === 'output_final' && responseDraftItem?.text) {
      thinkingItem = null;
      responseDraftItem = null;
    } else {
      folded.push(event);
    }
  }
  flushFolded();

  if (liveStreamState?.active && liveStreamState.thinkingText?.trim()) {
    items.push({
      kind: 'thinking',
      key: 'assistant-thinking',
      text: liveStreamState.thinkingText,
      live: true,
    });
  }

  if (liveStreamState?.active && liveStreamState.responseText?.trim()) {
    items.push({
      kind: 'response-draft',
      key: 'assistant-response-draft',
      label: agentLabel,
      text: liveStreamState.responseText,
      live: true,
    });
  }

  if (liveStreamState?.active && !liveStreamState.thinkingText?.trim() && !liveStreamState.responseText?.trim()) {
    items.push({
      kind: 'streaming',
      key: 'assistant-streaming',
      label: agentLabel,
    });
  }

  return items;
}

function latestRunnerState(events) {
  for (let i = events.length - 1; i >= 0; i -= 1) {
    const event = events[i];
    if (event.stream === 'status' && event.type === 'local_runner_attached') {
      return event.payload || {};
    }
  }
  return {};
}

function runnerStateValue(runnerState, snakeKey, camelKey) {
  return infoValue(runnerState?.[snakeKey] || runnerState?.[camelKey]);
}

function infoValue(value) {
  if (Array.isArray(value)) {
    return value.join(' ');
  }
  if (value === null || value === undefined) {
    return '';
  }
  return String(value).trim();
}

function buildRunningAgentInfoRows(runner, session, runnerState) {
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

function isConversationLocal(session) {
  return session?.availability === LOCAL_CONVERSATION_AVAILABILITY || isConversationPendingLocal(session);
}

function isConversationCloudOnly(session) {
  return session?.availability === CLOUD_ONLY_CONVERSATION_AVAILABILITY;
}

function isConversationPendingLocal(session) {
  if (session?.availability === PENDING_LOCAL_CONVERSATION_AVAILABILITY) {
    return true;
  }
  return session?.availability === 'unknown'
    && String(session?.provider || '').trim().toLowerCase() === 'local'
    && Boolean(String(session?.runnerId || '').trim());
}

function conversationAvailabilityLabel(session) {
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

function conversationAvailabilityMessage(session) {
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

function conversationAvailabilityRank(session) {
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

function writeAgentSessionURL(sessionId, { replace = false } = {}) {
  if (typeof window === 'undefined') {
    return;
  }
  const nextSessionId = String(sessionId || '').trim();
  try {
    const url = new URL(window.location.href);
    if (!url.pathname.endsWith('/agents')) {
      return;
    }
    if (nextSessionId) {
      url.searchParams.set('session', nextSessionId);
    } else {
      url.searchParams.delete('session');
    }
    const nextPath = `${url.pathname}${url.search}${url.hash}`;
    const currentPath = `${window.location.pathname}${window.location.search}${window.location.hash}`;
    if (nextPath === currentPath) {
      return;
    }
    const method = replace ? 'replaceState' : 'pushState';
    window.history[method](window.history.state, '', nextPath);
  } catch {
    // URL state is best-effort; selection still works without it.
  }
}

export default function SliceAgentsPage({
  sliceId,
  routeSessionId = '',
  slices,
  publicApiBaseUrl = '',
  onOpenCode,
  onOpenCommits,
  onOpenChangesets,
  onSelectSession,
}) {
  const agentsLayoutRef = useRef(null);
  const [sessions, setSessions] = useState([]);
  const [runners, setRunners] = useState([]);
  const [selectedSessionId, setSelectedSessionId] = useState('');
  const [selectedRunnerId, setSelectedRunnerId] = useState('');
  const [events, setEvents] = useState([]);
  const [runnersLoading, setRunnersLoading] = useState(false);
  const [sessionsLoading, setSessionsLoading] = useState(false);
  const [eventsLoading, setEventsLoading] = useState(false);
  const [creatingSession, setCreatingSession] = useState(false);
  const [inputText, setInputText] = useState('');
  const [sendingInput, setSendingInput] = useState(false);
  const [agentInfoOpen, setAgentInfoOpen] = useState(false);
  const [runnerActionLoading, setRunnerActionLoading] = useState(false);
  const [localChangesRequesting, setLocalChangesRequesting] = useState(false);
  const [exportingChangeset, setExportingChangeset] = useState(false);
  const [pendingLocalChangesRequestId, setPendingLocalChangesRequestId] = useState('');
  const [pendingLocalChangesRequestedAt, setPendingLocalChangesRequestedAt] = useState(0);
  const [pendingChangesetExportRequestId, setPendingChangesetExportRequestId] = useState('');
  const [changesetMessage, setChangesetMessage] = useState('');
  const [sessionsError, setSessionsError] = useState('');
  const [eventsError, setEventsError] = useState('');
  const [createError, setCreateError] = useState('');
  const [inputError, setInputError] = useState('');
  const [runnerActionError, setRunnerActionError] = useState('');
  const [localChangesError, setLocalChangesError] = useState('');
  const [runnersError, setRunnersError] = useState('');
  const [capabilities, setCapabilities] = useState(null);
  const [sessionsSidebarOpen, setSessionsSidebarOpen] = useState(true);
  const [sessionsSidebarDismissing, setSessionsSidebarDismissing] = useState(false);
  const [sessionsSidebarViewportSynced, setSessionsSidebarViewportSynced] = useState(false);
  const [agentsSidebarWidth, setAgentsSidebarWidth] = useState(readAgentsSidebarWidth);
  const [agentsSidebarResizing, setAgentsSidebarResizing] = useState(false);
  const [localChangesPanelOpen, setLocalChangesPanelOpen] = useState(true);
  const autoLocalChangesSessionRef = useRef('');
  const autoLocalChangesOutputSeqRef = useRef(0);

  const currentSlice = useMemo(() => (
    (slices || []).find((slice) => slice.slice_id === sliceId) || null
  ), [sliceId, slices]);
  const sliceLabel = getSliceDisplayName(currentSlice?.name || sliceId || 'Slice');
  const normalizedRouteSessionId = String(routeSessionId || '').trim();
  const defaultAgentType = capabilities?.defaultAgentType || capabilities?.default_agent_type || '';
  const onlineRunners = useMemo(() => runners.filter((runner) => runner.status === 'online'), [runners]);
  const onlineRunnerIds = useMemo(
    () => new Set(onlineRunners.map((runner) => runner.runnerId).filter(Boolean)),
    [onlineRunners],
  );
  const runnerSessions = useMemo(
    () => sessions.filter((session) => session.runnerId && onlineRunnerIds.has(session.runnerId)),
    [onlineRunnerIds, sessions],
  );
  const sessionsByRunnerId = useMemo(() => {
    const grouped = new Map();
    for (const session of runnerSessions) {
      const group = grouped.get(session.runnerId) || [];
      group.push(session);
      grouped.set(session.runnerId, group);
    }
    for (const group of grouped.values()) {
      group.sort((a, b) => (
        conversationAvailabilityRank(a) - conversationAvailabilityRank(b)
        || String(b.lastActivityAt || b.createdAt).localeCompare(String(a.lastActivityAt || a.createdAt))
      ));
    }
    return grouped;
  }, [runnerSessions]);
  const routeSession = useMemo(() => (
    normalizedRouteSessionId
      ? runnerSessions.find((session) => session.sessionId === normalizedRouteSessionId) || null
      : null
  ), [runnerSessions, normalizedRouteSessionId]);
  const selectedRunner = onlineRunners.find((runner) => runner.runnerId === selectedRunnerId)
    || (routeSession?.runnerId ? onlineRunners.find((runner) => runner.runnerId === routeSession.runnerId) : null)
    || onlineRunners[0]
    || null;
  const selectedRunnerSessions = selectedRunner
    ? sessionsByRunnerId.get(selectedRunner.runnerId) || []
    : [];
  const canCreateSession = Boolean(sliceId && selectedRunner?.runnerId);
  const selectedSession = selectedRunnerSessions.find((session) => session.sessionId === selectedSessionId) || null;
  const runnerState = useMemo(() => latestRunnerState(events), [events]);
  const runningAgentInfoRows = useMemo(
    () => buildRunningAgentInfoRows(selectedRunner, selectedSession, runnerState),
    [runnerState, selectedRunner, selectedSession],
  );
  const runnerHost = runnerStateValue(runnerState, 'host_name', 'hostName') || infoValue(selectedRunner?.hostName);
  const runnerPID = infoValue(runnerState?.pid || selectedRunner?.pid);
  const runnerRunningDir = runnerStateValue(runnerState, 'running_dir', 'runningDir')
    || runnerStateValue(runnerState, 'checkout_dir', 'checkoutDir')
    || infoValue(selectedRunner?.workspaceRoot);
  const runningAgentSummary = selectedRunner
    ? [
      selectedRunner.agentType || 'agent',
      runnerHost || 'local',
      runnerPID ? `pid ${runnerPID}` : '',
    ].filter(Boolean).join(' · ')
    : 'No running agent online';
  const selectedRunnerLocalCount = selectedRunnerSessions.filter(isConversationLocal).length;
  const selectedRunnerCloudOnlyCount = selectedRunnerSessions.filter(isConversationCloudOnly).length;
  const selectedRunnerConversationCountLabel = [
    selectedRunnerLocalCount === 1 ? '1 local conversation' : `${selectedRunnerLocalCount} local conversations`,
    selectedRunnerCloudOnlyCount > 0 ? `${selectedRunnerCloudOnlyCount} cloud-only` : '',
  ].filter(Boolean).join(' · ');
  const canRestartRunner = Boolean(
    selectedSession
    && selectedSession.provider === 'local'
    && selectedRunner?.runnerId
    && isConversationLocal(selectedSession),
  );
  const canSendInput = Boolean(selectedSessionId && selectedSession && isConversationLocal(selectedSession));
  const liveStreamState = useMemo(
    () => buildLiveStreamState(events, selectedSession),
    [events, selectedSession],
  );
  const assistantStreaming = liveStreamState.active;
  const localChangesEvent = useMemo(() => latestLocalChangesEvent(events), [events]);
  const localChangesFailureEvent = useMemo(() => latestLocalChangesFailureEvent(events), [events]);
  const localChanges = useMemo(() => (
    localChangesEvent ? normalizeLocalChangesPayload(localChangesEvent.payload) : null
  ), [localChangesEvent]);
  const latestExportEvent = useMemo(() => latestChangesetExportEvent(events), [events]);
  const latestExportFailureEvent = useMemo(() => latestChangesetExportFailureEvent(events), [events]);
  const latestAgentOutputFinalSeq = useMemo(
    () => latestEventSeq(events, (event) => event.stream === 'agent' && event.type === 'output_final'),
    [events],
  );
  const localChangesLoading = localChangesRequesting || Boolean(pendingLocalChangesRequestId);
  const changesetExportLoading = exportingChangeset || Boolean(pendingChangesetExportRequestId);
  const hasDirtyFiles = Boolean(localChanges && localChanges.pathCount > 0);
  const canExportChangeset = Boolean(
    canSendInput
    && hasDirtyFiles
    && !assistantStreaming
    && !changesetExportLoading
  );
  const latestChangesetExportPayload = latestExportEvent?.payload || {};
  const latestExportedChangesetId = latestChangesetExportPayload.changeset_id || latestChangesetExportPayload.changesetId || '';
  const latestLocalChangesFailureMessage = localChangesFailureEvent && localChangesFailureEvent.seq > (localChangesEvent?.seq || 0)
    ? localChangesFailureEvent.payload?.message || ''
    : '';
  const latestExportFailureMessage = latestExportFailureEvent && latestExportFailureEvent.seq > (latestExportEvent?.seq || 0)
    ? latestExportFailureEvent.payload?.message || ''
    : '';
  const localChangesDisplayError = localChangesError
    || latestLocalChangesFailureMessage
    || latestExportFailureMessage
    || '';
  const conversationItems = useMemo(
    () => buildConversationItems(events, liveStreamState, selectedSession),
    [events, liveStreamState, selectedSession],
  );
  const hasRunnerConversation = runnerSessions.length > 0;
  const showAgentSessionDocsLink = !sessionsLoading && !sessionsError && !hasRunnerConversation;
  const sessionsSidebarVisible = sessionsSidebarOpen || sessionsSidebarDismissing;

  const openSessionsSidebar = useCallback(() => {
    setSessionsSidebarDismissing(false);
    setSessionsSidebarOpen(true);
  }, []);

  const closeSessionsSidebar = useCallback(() => {
    if (typeof window !== 'undefined' && window.innerWidth <= 900) {
      setSessionsSidebarDismissing(true);
    } else {
      setSessionsSidebarDismissing(false);
    }
    setSessionsSidebarOpen(false);
  }, []);

  const resizeAgentsSidebar = useCallback((clientX) => {
    const rect = agentsLayoutRef.current?.getBoundingClientRect();
    if (!rect) {
      return;
    }
    const availableMaxWidth = Math.min(AGENTS_SIDEBAR_MAX_WIDTH, Math.max(AGENTS_SIDEBAR_MIN_WIDTH, rect.width - 420));
    setAgentsSidebarWidth(clampAgentsSidebarWidth(clientX - rect.left, availableMaxWidth));
  }, []);

  const handleAgentsSidebarResizePointerDown = useCallback((event) => {
    if (event.button !== undefined && event.button !== 0) {
      return;
    }
    if (typeof window !== 'undefined' && window.innerWidth <= SESSIONS_SIDEBAR_MOBILE_MAX_WIDTH) {
      return;
    }
    event.preventDefault();
    resizeAgentsSidebar(event.clientX);
    setAgentsSidebarResizing(true);
  }, [resizeAgentsSidebar]);

  const handleAgentsSidebarResizeKeyDown = useCallback((event) => {
    let nextWidth = null;
    const step = event.shiftKey ? 32 : 16;
    if (event.key === 'ArrowLeft') {
      nextWidth = agentsSidebarWidth - step;
    } else if (event.key === 'ArrowRight') {
      nextWidth = agentsSidebarWidth + step;
    } else if (event.key === 'Home') {
      nextWidth = AGENTS_SIDEBAR_MIN_WIDTH;
    } else if (event.key === 'End') {
      nextWidth = AGENTS_SIDEBAR_MAX_WIDTH;
    } else if (event.key === 'Enter') {
      nextWidth = AGENTS_SIDEBAR_DEFAULT_WIDTH;
    }
    if (nextWidth === null) {
      return;
    }
    event.preventDefault();
    setAgentsSidebarWidth(clampAgentsSidebarWidth(nextWidth));
  }, [agentsSidebarWidth]);

  const selectSessionId = useCallback((sessionId) => {
    setSelectedSessionId(sessionId);
    if (onSelectSession) {
      onSelectSession(sessionId);
    } else {
      writeAgentSessionURL(sessionId);
    }
  }, [onSelectSession]);

  const handleRunnerSelect = useCallback((runnerId) => {
    setSelectedRunnerId(runnerId);
    const nextSessionId = (sessionsByRunnerId.get(runnerId) || [])[0]?.sessionId || '';
    selectSessionId(nextSessionId);
  }, [selectSessionId, sessionsByRunnerId]);

  const handleSessionSelect = useCallback((sessionId) => {
    const nextSession = selectedRunnerSessions.find((session) => session.sessionId === sessionId);
    if (nextSession?.runnerId) {
      setSelectedRunnerId(nextSession.runnerId);
    }
    selectSessionId(sessionId);
    if (typeof window !== 'undefined' && window.innerWidth <= SESSIONS_SIDEBAR_MOBILE_MAX_WIDTH) {
      closeSessionsSidebar();
    }
  }, [closeSessionsSidebar, selectSessionId, selectedRunnerSessions]);

  useIsomorphicLayoutEffect(() => {
    if (typeof window === 'undefined') {
      return undefined;
    }
    const syncSidebarForViewport = () => {
      if (window.innerWidth > SESSIONS_SIDEBAR_MOBILE_MAX_WIDTH) {
        setSessionsSidebarDismissing(false);
        setSessionsSidebarOpen(true);
      } else {
        setSessionsSidebarOpen(false);
      }
    };
    syncSidebarForViewport();
    setSessionsSidebarViewportSynced(true);
    window.addEventListener('resize', syncSidebarForViewport);
    return () => window.removeEventListener('resize', syncSidebarForViewport);
  }, []);

  useEffect(() => {
    if (!sessionsSidebarDismissing || typeof window === 'undefined') {
      return undefined;
    }
    const timeoutId = window.setTimeout(() => {
      setSessionsSidebarDismissing(false);
    }, 280);
    return () => window.clearTimeout(timeoutId);
  }, [sessionsSidebarDismissing]);

  useEffect(() => {
    writeAgentsSidebarWidth(agentsSidebarWidth);
  }, [agentsSidebarWidth]);

  useEffect(() => {
    if (!agentsSidebarResizing || typeof window === 'undefined') {
      return undefined;
    }
    const previousCursor = document.body.style.cursor;
    const previousUserSelect = document.body.style.userSelect;
    const handlePointerMove = (event) => {
      resizeAgentsSidebar(event.clientX);
    };
    const finishResize = () => {
      setAgentsSidebarResizing(false);
    };

    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
    window.addEventListener('pointermove', handlePointerMove);
    window.addEventListener('pointerup', finishResize);
    window.addEventListener('pointercancel', finishResize);

    return () => {
      document.body.style.cursor = previousCursor;
      document.body.style.userSelect = previousUserSelect;
      window.removeEventListener('pointermove', handlePointerMove);
      window.removeEventListener('pointerup', finishResize);
      window.removeEventListener('pointercancel', finishResize);
    };
  }, [agentsSidebarResizing, resizeAgentsSidebar]);

  useEffect(() => {
    if (!routeSession) {
      return;
    }
    if (routeSession.runnerId && onlineRunnerIds.has(routeSession.runnerId)) {
      setSelectedRunnerId(routeSession.runnerId);
    }
    setSelectedSessionId(routeSession.sessionId);
  }, [onlineRunnerIds, routeSession]);

  const loadRunners = useCallback(async ({ keepSelection = true } = {}) => {
    setRunnersLoading(true);
    setRunnersError('');
    try {
      const nextRunners = (await listAgentRunners({ limit: 50, includeOffline: true })).map(normalizeRunner);
      setRunners(nextRunners);
      setSelectedRunnerId((current) => {
        if (keepSelection && current && nextRunners.some((runner) => runner.runnerId === current && runner.status === 'online')) {
          return current;
        }
        return nextRunners.find((runner) => runner.status === 'online')?.runnerId || '';
      });
    } catch (err) {
      setRunners([]);
      setSelectedRunnerId('');
      setRunnersError(err?.message || 'Unable to load running agents.');
    } finally {
      setRunnersLoading(false);
    }
  }, []);

  const loadSessions = useCallback(async ({ keepSelection = false } = {}) => {
    if (!sliceId) {
      setSessions([]);
      setSelectedSessionId('');
      return;
    }
    setSessionsLoading(true);
    setSessionsError('');
    try {
      const nextSessions = (await listAgentSessions(sliceId, { limit: 50 })).map(normalizeSession);
      setSessions(nextSessions);
      setSelectedSessionId((current) => {
        if (normalizedRouteSessionId && nextSessions.some((session) => session.sessionId === normalizedRouteSessionId)) {
          return normalizedRouteSessionId;
        }
        if (keepSelection && current && nextSessions.some((session) => session.sessionId === current)) {
          return current;
        }
        return current && nextSessions.some((session) => session.sessionId === current) ? current : '';
      });
    } catch (err) {
      setSessions([]);
      setSelectedSessionId('');
      setSessionsError(err?.message || 'Unable to load agent sessions.');
    } finally {
      setSessionsLoading(false);
    }
  }, [normalizedRouteSessionId, sliceId]);

  useEffect(() => {
    let active = true;
    getAgentCapabilities()
      .then((nextCapabilities) => {
        if (active) setCapabilities(nextCapabilities || null);
      })
      .catch(() => {
        if (active) setCapabilities(null);
      });
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    let active = true;
    const refresh = async () => {
      if (!active) return;
      await loadRunners();
    };
    refresh();
    const intervalId = window.setInterval(refresh, 10000);
    return () => {
      active = false;
      window.clearInterval(intervalId);
    };
  }, [loadRunners]);

  useEffect(() => {
    loadSessions();
  }, [loadSessions]);

  useEffect(() => {
    if (!selectedRunner) {
      if (selectedSessionId) {
        setSelectedSessionId('');
      }
      return;
    }
    if (routeSession?.runnerId && selectedRunner.runnerId !== routeSession.runnerId) {
      return;
    }
    if (selectedSessionId && selectedRunnerSessions.some((session) => session.sessionId === selectedSessionId)) {
      return;
    }
    const fallbackSessionId = selectedRunnerSessions[0]?.sessionId || '';
    if (selectedSessionId !== fallbackSessionId) {
      setSelectedSessionId(fallbackSessionId);
    }
  }, [routeSession, selectedRunner, selectedRunnerSessions, selectedSessionId]);

  const loadSelectedEvents = useCallback(async () => {
    if (!selectedSessionId) {
      setEvents([]);
      setEventsError('');
      setEventsLoading(false);
      return;
    }

    setEventsLoading(true);
    setEventsError('');
    try {
      let sinceSeq = 0;
      const nextEvents = [];
      while (nextEvents.length < AGENT_EVENTS_MAX) {
        const payload = await listAgentSessionEvents(selectedSessionId, {
          sinceSeq,
          limit: Math.min(AGENT_EVENTS_PAGE_SIZE, AGENT_EVENTS_MAX - nextEvents.length),
        });
        const pageEvents = payload?.events || [];
        nextEvents.push(...pageEvents);
        const nextSeq = Number(payload?.nextSeq ?? payload?.next_seq ?? 0);
        if (pageEvents.length < AGENT_EVENTS_PAGE_SIZE || !Number.isFinite(nextSeq) || nextSeq <= sinceSeq) {
          break;
        }
        sinceSeq = nextSeq - 1;
      }
      setEvents(nextEvents.map(normalizeEvent));
    } catch (err) {
      setEvents([]);
      setEventsError(err?.message || 'Unable to load agent conversation.');
    } finally {
      setEventsLoading(false);
    }
  }, [selectedSessionId]);

  const requestLocalChanges = useCallback(async ({ silent = false } = {}) => {
    if (!selectedSessionId || !selectedSession || !isConversationLocal(selectedSession)) {
      return;
    }
    setLocalChangesRequesting(true);
    if (!silent) {
      setLocalChangesError('');
    }
    try {
      const result = await requestAgentSessionLocalChanges(selectedSessionId, { limit: 100 });
      setPendingLocalChangesRequestId(result.requestId || '');
      setPendingLocalChangesRequestedAt(Date.now());
      await loadSelectedEvents();
    } catch (err) {
      setLocalChangesError(err?.message || 'Unable to refresh local changes.');
      setPendingLocalChangesRequestId('');
      setPendingLocalChangesRequestedAt(0);
    } finally {
      setLocalChangesRequesting(false);
    }
  }, [loadSelectedEvents, selectedSession, selectedSessionId]);

  const handleExportChangeset = useCallback(async () => {
    if (!canExportChangeset || !selectedSessionId) {
      return;
    }
    setExportingChangeset(true);
    setLocalChangesError('');
    try {
      const result = await requestAgentSessionChangesetExport(selectedSessionId, {
        message: changesetMessage.trim(),
      });
      setPendingChangesetExportRequestId(result.requestId || '');
      await loadSelectedEvents();
    } catch (err) {
      setLocalChangesError(err?.message || 'Unable to export changeset.');
    } finally {
      setExportingChangeset(false);
    }
  }, [canExportChangeset, changesetMessage, loadSelectedEvents, selectedSessionId]);

  useEffect(() => {
    if (!selectedSessionId) {
      loadSelectedEvents();
      return undefined;
    }

    let active = true;
    const pollIntervalMs = assistantStreaming || localChangesLoading || changesetExportLoading ? 1000 : 5000;
    const loadEvents = async () => {
      if (!active) return;
      await loadSelectedEvents();
    };

    loadEvents();
    const intervalId = window.setInterval(loadEvents, pollIntervalMs);
    return () => {
      active = false;
      window.clearInterval(intervalId);
    };
  }, [assistantStreaming, changesetExportLoading, loadSelectedEvents, localChangesLoading, selectedSessionId]);

  useEffect(() => {
    setRunnerActionError('');
    setLocalChangesError('');
    setPendingLocalChangesRequestId('');
    setPendingLocalChangesRequestedAt(0);
    setPendingChangesetExportRequestId('');
    setChangesetMessage('');
    autoLocalChangesOutputSeqRef.current = 0;
    setEvents([]);
    setEventsError('');
  }, [selectedSessionId]);

  useEffect(() => {
    setAgentInfoOpen(false);
    setRunnerActionError('');
  }, [selectedRunnerId]);

  useEffect(() => {
    if (!pendingLocalChangesRequestId) {
      return;
    }
    const matched = [localChangesEvent, localChangesFailureEvent]
      .filter(Boolean)
      .some((event) => payloadRequestId(event.payload) === pendingLocalChangesRequestId);
    if (matched) {
      setPendingLocalChangesRequestId('');
      setPendingLocalChangesRequestedAt(0);
      if (localChangesEvent && payloadRequestId(localChangesEvent.payload) === pendingLocalChangesRequestId) {
        setLocalChangesError('');
      }
    }
  }, [localChangesEvent, localChangesFailureEvent, pendingLocalChangesRequestId]);

  useEffect(() => {
    if (!pendingLocalChangesRequestId || !pendingLocalChangesRequestedAt || typeof window === 'undefined') {
      return undefined;
    }
    const elapsed = Date.now() - pendingLocalChangesRequestedAt;
    const timeoutDelay = Math.max(0, LOCAL_CHANGES_REQUEST_TIMEOUT_MS - elapsed);
    const timeoutId = window.setTimeout(() => {
      setPendingLocalChangesRequestId('');
      setPendingLocalChangesRequestedAt(0);
      setLocalChangesRequesting(false);
      setLocalChangesError('Runner did not return local changes.');
    }, timeoutDelay);
    return () => window.clearTimeout(timeoutId);
  }, [pendingLocalChangesRequestId, pendingLocalChangesRequestedAt]);

  useEffect(() => {
    if (!pendingChangesetExportRequestId) {
      return;
    }
    const matched = [latestExportEvent, latestExportFailureEvent]
      .filter(Boolean)
      .some((event) => payloadRequestId(event.payload) === pendingChangesetExportRequestId);
    if (matched) {
      setPendingChangesetExportRequestId('');
      setExportingChangeset(false);
      if (latestExportEvent && payloadRequestId(latestExportEvent.payload) === pendingChangesetExportRequestId) {
        setChangesetMessage('');
      }
    }
  }, [latestExportEvent, latestExportFailureEvent, pendingChangesetExportRequestId]);

  useEffect(() => {
    if (!selectedSessionId || !selectedSession || !isConversationLocal(selectedSession)) {
      return;
    }
    if (autoLocalChangesSessionRef.current === selectedSessionId) {
      return;
    }
    autoLocalChangesSessionRef.current = selectedSessionId;
    requestLocalChanges({ silent: true });
  }, [requestLocalChanges, selectedSession, selectedSessionId]);

  useEffect(() => {
    if (!selectedSessionId || !selectedSession || !isConversationLocal(selectedSession) || assistantStreaming) {
      return;
    }
    if (latestAgentOutputFinalSeq === 0 || autoLocalChangesOutputSeqRef.current >= latestAgentOutputFinalSeq) {
      return;
    }
    autoLocalChangesOutputSeqRef.current = latestAgentOutputFinalSeq;
    requestLocalChanges({ silent: true });
  }, [assistantStreaming, latestAgentOutputFinalSeq, requestLocalChanges, selectedSession, selectedSessionId]);

  useEffect(() => {
    if (!agentInfoOpen || typeof window === 'undefined') {
      return undefined;
    }
    const handleKeyDown = (event) => {
      if (event.key === 'Escape') {
        setAgentInfoOpen(false);
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [agentInfoOpen]);

  const handleCreateSession = async () => {
    if (!canCreateSession || creatingSession) {
      return;
    }
    setCreatingSession(true);
    setCreateError('');
    try {
      const created = normalizeSession(await createAgentSession(sliceId, {
        runnerId: selectedRunner.runnerId,
        agentType: selectedRunner.agentType || defaultAgentType,
      }));
      await loadSessions({ keepSelection: true });
      if (created.runnerId || selectedRunner.runnerId) {
        setSelectedRunnerId(created.runnerId || selectedRunner.runnerId);
      }
      selectSessionId(created.sessionId);
    } catch (err) {
      setCreateError(err?.message || 'Unable to create agent session.');
    } finally {
      setCreatingSession(false);
    }
  };

  const handleSendInput = async (event) => {
    event.preventDefault();
    const text = inputText.trim();
    if (!canSendInput || !text || sendingInput) {
      return;
    }
    setSendingInput(true);
    setInputError('');
    try {
      await sendAgentSessionInput(selectedSessionId, text);
      setInputText('');
      await loadSelectedEvents();
    } catch (err) {
      setInputError(err?.message || 'Unable to send agent input.');
    } finally {
      setSendingInput(false);
    }
  };

  const handleUpgradeRestartRunner = async () => {
    if (!selectedSessionId || runnerActionLoading || !canRestartRunner) {
      return;
    }
    setRunnerActionLoading(true);
    setRunnerActionError('');
    try {
      await requestAgentRunnerRestart(selectedSessionId, {
        upgrade: true,
        reason: 'web_ui',
      });
      await loadSelectedEvents();
    } catch (err) {
      setRunnerActionError(err?.message || 'Unable to request agent restart.');
    } finally {
      setRunnerActionLoading(false);
    }
  };

  const localChangesPanelAvailable = Boolean(selectedSession && isConversationLocal(selectedSession));
  const localChangesPanelVisible = Boolean(localChangesPanelAvailable && localChangesPanelOpen);
  const localChangesSection = localChangesPanelAvailable ? (
    <section className="slice-agents-local-changes" data-testid="slice-agents-local-changes">
      <div className="slice-agents-local-changes-header">
        <div className="slice-agents-local-changes-title">
          <FileDiff size={16} aria-hidden="true" />
          <div>
            <h2>Local changes</h2>
            <span>
              {localChangesLoading && !localChanges ? 'Checking' : localChangesSummaryText(localChanges)}
              {localChanges?.trackedChangesetId ? ` · tracked ${shortEntityId(localChanges.trackedChangesetId)}` : ''}
            </span>
          </div>
        </div>
        <div className="slice-agents-local-changes-actions">
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="slice-agents-icon-button"
            onClick={() => requestLocalChanges()}
            disabled={localChangesLoading}
            aria-label="Refresh local changes"
            title="Refresh local changes"
            data-testid="slice-agents-refresh-local-changes"
          >
            <RefreshCw size={15} aria-hidden="true" />
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="slice-agents-icon-button"
            onClick={() => setLocalChangesPanelOpen(false)}
            aria-label="Hide local changes"
            title="Hide local changes"
            data-testid="slice-agents-hide-local-changes"
          >
            <PanelRightClose size={15} aria-hidden="true" />
          </Button>
        </div>
      </div>
      {localChangesDisplayError && <div className="panel-error">{localChangesDisplayError}</div>}
      {localChangesLoading && !localChanges && !localChangesDisplayError && (
        <div className="slice-agents-local-clean" data-testid="slice-agents-local-checking">Checking local checkout...</div>
      )}
      {!localChangesLoading && !localChanges && !localChangesDisplayError && (
        <div className="slice-agents-local-clean" data-testid="slice-agents-local-not-loaded">Local changes not loaded.</div>
      )}
      {localChanges && localChanges.pathCount > 0 && (
        <ul className="slice-agents-local-file-list" data-testid="slice-agents-local-file-list">
          {localChanges.paths.map((entry) => (
            <li key={`${entry.status}-${entry.path}`} className="slice-agents-local-file">
              <span
                className={`slice-agents-local-file-status slice-agents-local-file-status--${entry.status.toLowerCase()}`}
                title={changeStatusLabel(entry.status)}
              >
                {entry.status}
              </span>
              <span className="slice-agents-local-file-path" title={entry.path}>{entry.path}</span>
            </li>
          ))}
          {localChanges.truncated && (
            <li className="slice-agents-local-file slice-agents-local-file--more">
              {localChanges.pathCount - localChanges.paths.length} more changed files
            </li>
          )}
        </ul>
      )}
      {localChanges && localChanges.pathCount === 0 && (
        <div className="slice-agents-local-clean" data-testid="slice-agents-local-clean">Working tree clean</div>
      )}
      {latestExportedChangesetId && (
        <a
          className="slice-agents-export-link"
          href={`/changesets/${encodeURIComponent(latestExportedChangesetId)}`}
          data-testid="slice-agents-exported-changeset"
        >
          <GitPullRequest size={14} aria-hidden="true" />
          <span>{shortEntityId(latestExportedChangesetId, 18)}</span>
          <ExternalLink size={13} aria-hidden="true" />
        </a>
      )}
      <div className="slice-agents-export-controls">
        <input
          className="slice-agents-export-message"
          value={changesetMessage}
          onChange={(event) => setChangesetMessage(event.target.value)}
          placeholder="Changeset message"
          disabled={!canSendInput || changesetExportLoading || assistantStreaming}
          data-testid="slice-agents-export-message"
        />
        <Button
          type="button"
          variant="default"
          size="sm"
          className="slice-agents-export-button"
          onClick={handleExportChangeset}
          disabled={!canExportChangeset}
          title={hasDirtyFiles ? 'Export local changes to a changeset' : 'No local changes to export'}
          data-testid="slice-agents-export-changeset"
        >
          <GitPullRequest size={15} aria-hidden="true" />
          {changesetExportLoading ? 'Exporting' : localChanges?.trackedChangesetId ? 'Update changeset' : 'Export changeset'}
        </Button>
      </div>
    </section>
  ) : null;

  return (
    <section
      className={`slice-agents-page ${sessionsSidebarViewportSynced ? 'viewport-synced' : 'viewport-pending'}`}
      data-testid="slice-agents-page"
    >
      <SliceDetailNav
        activeTab="agents"
        sliceId={sliceId}
        sliceLabel={sliceLabel}
        slice={currentSlice}
        publicApiBaseUrl={publicApiBaseUrl}
        onOpenCode={onOpenCode}
        onOpenCommits={onOpenCommits}
        onOpenChangesets={onOpenChangesets}
        onOpenAgents={() => {}}
      />

      <div
        ref={agentsLayoutRef}
        className={`slice-agents-layout${agentsSidebarResizing ? ' resizing-sidebar' : ''}`}
        style={{ '--slice-agents-sidebar-width': `${agentsSidebarWidth}px` }}
      >
        <div
          className={`slice-agents-sidebar-overlay${sessionsSidebarVisible ? ' visible' : ''}${sessionsSidebarDismissing ? ' dismissing' : ''}`}
          onClick={closeSessionsSidebar}
        />
        <aside
          className={`slice-agents-sidebar ${sessionsSidebarOpen ? 'open' : 'closed'}${sessionsSidebarDismissing ? ' dismissing' : ''}`}
          aria-label="Running agents and conversations"
        >
          <section className="slice-agents-panel slice-agents-running-agents" data-testid="slice-agents-runners">
            <div className="slice-agents-runner-toolbar">
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="slice-agents-icon-button slice-agents-sidebar-close"
                onClick={closeSessionsSidebar}
                aria-label="Close running agents and conversations"
                title="Close running agents and conversations"
              >
                <PanelLeftClose size={15} aria-hidden="true" />
              </Button>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="slice-agents-icon-button slice-agents-runner-refresh"
                onClick={() => {
                  loadRunners({ keepSelection: true });
                  loadSessions({ keepSelection: true });
                }}
                aria-label="Refresh running agents and conversations"
                title="Refresh"
              >
                <RefreshCw size={15} aria-hidden="true" />
              </Button>
            </div>
            {runnersLoading && runners.length === 0 && <div className="panel-empty">Loading running agents...</div>}
            {!runnersLoading && runnersError && <div className="panel-error">{runnersError}</div>}
            {!runnersLoading && !runnersError && runners.length === 0 && (
              <div className="panel-empty">No local runners found.</div>
            )}
            {!runnersLoading && !runnersError && runners.length > 0 && onlineRunners.length === 0 && (
              <div className="panel-empty">No running agents online.</div>
            )}
            {onlineRunners.length > 0 && (
              <div className="slice-agents-runner-tabs" role="tablist" aria-label="Running agents">
                {onlineRunners.map((runner) => {
                  const isSelected = selectedRunner?.runnerId === runner.runnerId;
                  const runnerName = runnerDisplayName(runner);
                  return (
                    <div
                      key={runner.runnerId}
                      className={`slice-agents-runner-tab${isSelected ? ' active' : ''}`}
                      title={runner.workspaceRoot || runner.runnerId}
                    >
                      <Button
                        type="button"
                        variant="ghost"
                        role="tab"
                        className="slice-agents-runner-tab-select"
                        onClick={() => handleRunnerSelect(runner.runnerId)}
                        aria-selected={isSelected}
                        data-testid="slice-agents-runner"
                      >
                        <span className="slice-agents-runner-tab-title">
                          {runnerName}
                        </span>
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        className="slice-agents-icon-button slice-agents-runner-tab-info"
                        onClick={() => {
                          handleRunnerSelect(runner.runnerId);
                          setAgentInfoOpen(true);
                        }}
                        aria-label={`Inspect ${runnerName} runner`}
                        aria-haspopup="dialog"
                        aria-expanded={agentInfoOpen && isSelected}
                        title="Inspect runner"
                        data-testid="slice-agents-info"
                      >
                        <Info size={14} aria-hidden="true" />
                      </Button>
                    </div>
                  );
                })}
              </div>
            )}
          </section>

          {!canCreateSession && (
            <div className="slice-agents-connection-note" data-testid="slice-agents-connection-note">
              <CircleAlert size={15} aria-hidden="true" />
              <span>Start a running agent to create conversations.</span>
            </div>
          )}
          {showAgentSessionDocsLink && (
            <div className="slice-agents-docs-note" data-testid="slice-agents-docs-note">
              <CircleAlert size={15} aria-hidden="true" />
              <span>
                No local conversation. <a href="/docs#local-agent-sessions">Start a running agent with gs.</a>
              </span>
            </div>
          )}

          <section className="slice-agents-panel slice-agents-session-panel">
            <div className="slice-agents-section-head">
              <div>
                <h3>{selectedRunner ? 'Conversations for this runner' : 'Conversations'}</h3>
                <span>{selectedRunner ? `${selectedRunnerConversationCountLabel} on this runner` : 'Select a running agent'}</span>
              </div>
              <Button
                type="button"
                variant="default"
                size="sm"
                className="slice-agents-new-button"
                onClick={handleCreateSession}
                disabled={!canCreateSession || creatingSession}
                title={canCreateSession ? 'New conversation' : 'Start a running agent first'}
                data-testid="slice-agents-new-session"
              >
                <Plus size={15} aria-hidden="true" />
                {creatingSession ? 'Starting' : 'New'}
              </Button>
            </div>
            {createError && <div className="panel-error">{createError}</div>}
            {sessionsError && <div className="panel-error">{sessionsError}</div>}
            {sessionsLoading && selectedRunnerSessions.length === 0 && <div className="panel-empty">Loading conversations...</div>}
            {!sessionsLoading && !sessionsError && !selectedRunner && (
              <div className="panel-empty">Select a running agent to view conversations.</div>
            )}
            {!sessionsLoading && !sessionsError && selectedRunner && selectedRunnerSessions.length === 0 && (
              <div className="panel-empty">No conversations for this running agent.</div>
            )}
            {selectedRunnerSessions.length > 0 && (
              <ul className="slice-agents-session-list">
                {selectedRunnerSessions.map((session) => {
                  const isSelected = session.sessionId === selectedSessionId;
                  const cloudOnly = isConversationCloudOnly(session);
                  return (
                    <li key={session.sessionId}>
                      <Button
                        type="button"
                        variant="ghost"
                        className={`slice-agents-session-row${isSelected ? ' active' : ''}${cloudOnly ? ' cloud-only' : ''}`}
                        onClick={() => handleSessionSelect(session.sessionId)}
                        aria-pressed={isSelected}
                        data-testid="slice-agents-session"
                      >
                        <span className="slice-agents-session-icon" aria-hidden="true">
                          {cloudOnly ? <CloudOff size={15} /> : <Bot size={15} />}
                        </span>
                        <span className="slice-agents-session-main">
                          <span className="slice-agents-session-title">
                            Conversation · {shortSessionId(session.sessionId)}
                          </span>
                          <span className="slice-agents-session-meta">
                            {[conversationAvailabilityLabel(session), session.agentType || 'agent', formatAgentTimestamp(session.lastActivityAt || session.createdAt)].filter(Boolean).join(' · ')}
                          </span>
                        </span>
                      </Button>
                    </li>
                  );
                })}
              </ul>
            )}
          </section>
        </aside>

        <div
          className="slice-agents-resize-handle"
          role="separator"
          aria-label="Resize agents and conversations panel"
          aria-orientation="vertical"
          aria-valuemin={AGENTS_SIDEBAR_MIN_WIDTH}
          aria-valuemax={AGENTS_SIDEBAR_MAX_WIDTH}
          aria-valuenow={Math.round(agentsSidebarWidth)}
          tabIndex={0}
          title="Drag to resize. Double-click to reset."
          data-testid="slice-agents-resize-handle"
          onPointerDown={handleAgentsSidebarResizePointerDown}
          onKeyDown={handleAgentsSidebarResizeKeyDown}
          onDoubleClick={() => setAgentsSidebarWidth(AGENTS_SIDEBAR_DEFAULT_WIDTH)}
        >
          <span aria-hidden="true" />
        </div>

        <main className="slice-agents-conversation" data-testid="slice-agents-conversation">
          <div className="slice-agents-mobile-toolbar">
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="slice-agents-icon-button"
              onClick={openSessionsSidebar}
              aria-label="Open running agents and conversations"
              title="Open running agents and conversations"
              data-testid="slice-agents-open-sessions"
            >
              <PanelLeftOpen size={16} aria-hidden="true" />
            </Button>
            <span>Running agents</span>
          </div>
          {!selectedSession && (
            <div className="slice-agents-empty-detail">
              <Bot size={22} aria-hidden="true" />
              <span>Select a conversation from the running agent.</span>
            </div>
          )}
          {selectedSession && (
            <div className={`slice-agents-conversation-shell${localChangesPanelVisible ? ' has-local-changes' : ''}`}>
              <div className="slice-agents-conversation-header">
                <div>
                  <h1>Conversation</h1>
                  <span>{conversationAvailabilityLabel(selectedSession)} · {selectedSession.agentType || 'agent'} · {selectedSession.sessionId}</span>
                </div>
                {isConversationLocal(selectedSession) && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="slice-agents-local-panel-toggle"
                    onClick={() => setLocalChangesPanelOpen((open) => !open)}
                    aria-expanded={localChangesPanelOpen}
                    aria-controls="slice-agents-local-changes-panel"
                    title={localChangesPanelOpen ? 'Hide local changes' : 'Show local changes'}
                    data-testid="slice-agents-toggle-local-changes"
                  >
                    {localChangesPanelOpen ? (
                      <PanelRightClose size={15} aria-hidden="true" />
                    ) : (
                      <PanelRightOpen size={15} aria-hidden="true" />
                    )}
                    <span>Local changes</span>
                  </Button>
                )}
              </div>
              <div className="slice-agents-conversation-body">
                <section className="slice-agents-conversation-thread">
                  {!isConversationLocal(selectedSession) && (
                    <div className="slice-agents-connection-note" data-testid="slice-agents-local-availability-note">
                      <CircleAlert size={15} aria-hidden="true" />
                      <span>{conversationAvailabilityMessage(selectedSession)}</span>
                    </div>
                  )}
                  {eventsError && <div className="panel-error">{eventsError}</div>}
                  {eventsLoading && events.length === 0 && <div className="panel-empty">Loading conversation...</div>}
                  {!eventsLoading && !eventsError && conversationItems.length === 0 && (
                    <div className="slice-agents-empty-detail">
                      <TerminalSquare size={22} aria-hidden="true" />
                      <span>No messages yet.</span>
                    </div>
                  )}
                  {conversationItems.length > 0 && (
                    <ol className="slice-agents-message-list" data-testid="slice-agents-message-list">
                      {conversationItems.map((item) => (
                        item.kind === 'message' ? (
                          <li key={item.key} className={`slice-agents-message slice-agents-message--${item.message.role}`}>
                            <div className="slice-agents-message-header">
                              <span>{item.message.label}</span>
                              <time>{formatAgentTimestamp(item.message.ts)}</time>
                            </div>
                            <div
                              className="slice-agents-message-body slice-agents-message-markdown"
                              dangerouslySetInnerHTML={{ __html: renderConversationMarkdown(item.message.text) }}
                            />
                          </li>
                        ) : item.kind === 'thinking' || item.kind === 'response-draft' ? (
                          <li
                            key={item.key}
                            className={[
                              'slice-agents-message',
                              'slice-agents-message--assistant',
                              item.live ? 'slice-agents-message--live' : '',
                              `slice-agents-message--${item.kind}`,
                            ].filter(Boolean).join(' ')}
                            aria-live={item.live ? 'polite' : undefined}
                            data-testid={`slice-agents-${item.kind}`}
                          >
                            <div className="slice-agents-message-header">
                              <span>{item.kind === 'thinking' ? 'Thinking' : item.label}</span>
                              {item.live ? (
                                <span className="slice-agents-streaming-label">{item.kind === 'thinking' ? 'Working' : 'Streaming'}</span>
                              ) : item.ts ? (
                                <time>{formatAgentTimestamp(item.ts)}</time>
                              ) : null}
                            </div>
                            <div
                              className="slice-agents-message-body slice-agents-message-markdown"
                              dangerouslySetInnerHTML={{ __html: renderConversationMarkdown(item.text) }}
                            />
                          </li>
                        ) : item.kind === 'streaming' ? (
                          <li
                            key={item.key}
                            className="slice-agents-message slice-agents-message--assistant slice-agents-message--streaming"
                            aria-live="polite"
                            data-testid="slice-agents-streaming"
                          >
                            <div className="slice-agents-message-header">
                              <span>{item.label}</span>
                              <span className="slice-agents-streaming-label">Responding</span>
                            </div>
                            <div className="slice-agents-streaming-dots" aria-label={`${item.label} is responding`}>
                              <span />
                              <span />
                              <span />
                            </div>
                          </li>
                        ) : (
                          <li key={item.key} className="slice-agents-timeline-events">
                            <details className="slice-agents-debug-events">
                              <summary>Agent activity ({item.events.length})</summary>
                              <ol className="slice-agents-event-list">
                                {item.events.map((event) => (
                                  <li
                                    key={`${event.seq}-${event.stream}-${event.type}`}
                                    className={`slice-agents-event slice-agents-event--${eventTone(event)}`}
                                  >
                                    <div className="slice-agents-event-header">
                                      <span>{eventTitle(event)}</span>
                                      <time>{formatAgentTimestamp(event.ts)}</time>
                                    </div>
                                    <pre>{eventBody(event)}</pre>
                                  </li>
                                ))}
                              </ol>
                            </details>
                          </li>
                        )
                      ))}
                    </ol>
                  )}
                  {inputError && <div className="panel-error">{inputError}</div>}
                  <form className="slice-agents-input-form" onSubmit={handleSendInput}>
                    <input
                      className="slice-agents-input"
                      data-testid="slice-agents-input"
                      value={inputText}
                      onChange={(event) => setInputText(event.target.value)}
                      placeholder={canSendInput ? 'Message agent' : 'Local copy unavailable'}
                      disabled={sendingInput || !canSendInput}
                    />
                    <Button
                      type="submit"
                      variant="default"
                      size="icon"
                      className="slice-agents-send-button"
                      disabled={!inputText.trim() || sendingInput || !canSendInput}
                      aria-label="Send message"
                      title="Send"
                      data-testid="slice-agents-send"
                    >
                      <Send size={16} aria-hidden="true" />
                    </Button>
                  </form>
                </section>
              </div>
              {localChangesPanelAvailable && (
                <aside
                  id="slice-agents-local-changes-panel"
                  className={`slice-agents-local-changes-panel${localChangesPanelVisible ? ' open' : ''}`}
                  aria-hidden={!localChangesPanelVisible}
                  data-testid="slice-agents-local-changes-panel"
                >
                  {localChangesSection}
                </aside>
              )}
            </div>
          )}
        </main>
      </div>
      {agentInfoOpen && selectedRunner && (
        <div className="slice-agents-info-dialog-backdrop" role="presentation" onClick={() => setAgentInfoOpen(false)}>
          <div
            className="slice-agents-info-dialog"
            role="dialog"
            aria-modal="true"
            aria-labelledby="slice-agents-info-dialog-title"
            data-testid="slice-agents-info-dialog"
            onClick={(event) => event.stopPropagation()}
          >
            <div className="slice-agents-info-dialog-header">
              <div>
                <h2 id="slice-agents-info-dialog-title">Agent runner</h2>
                <span>{runningAgentSummary}</span>
              </div>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="slice-agents-icon-button"
                onClick={() => setAgentInfoOpen(false)}
                aria-label="Close agent runner details"
                title="Close"
              >
                <X size={15} aria-hidden="true" />
              </Button>
            </div>
            {runnerRunningDir && (
              <div className="slice-agents-runner-dir" title={runnerRunningDir}>
                {runnerRunningDir}
              </div>
            )}
            <div className="slice-agents-info-panel" data-testid="slice-agents-info-panel">
              <dl>
                {runningAgentInfoRows.map(([label, value]) => (
                  <div key={label} className="slice-agents-info-row">
                    <dt>{label}</dt>
                    <dd>{value}</dd>
                  </div>
                ))}
              </dl>
            </div>
            <div className="slice-agents-info-dialog-actions">
              <Button
                type="button"
                variant="default"
                size="sm"
                className="slice-agents-runner-action"
                onClick={handleUpgradeRestartRunner}
                disabled={!canRestartRunner || runnerActionLoading}
                title={canRestartRunner ? 'Upgrade and restart running agent' : 'Select an active session'}
                data-testid="slice-agents-upgrade-restart"
              >
                <RefreshCw size={15} aria-hidden="true" />
                {runnerActionLoading ? 'Requesting' : 'Upgrade & restart'}
              </Button>
            </div>
            {runnerActionError && <div className="panel-error">{runnerActionError}</div>}
          </div>
        </div>
      )}
    </section>
  );
}
