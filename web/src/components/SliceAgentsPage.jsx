import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import {
  Bot,
  CircleAlert,
  CloudOff,
  Info,
  PanelLeftClose,
  PanelLeftOpen,
  Plus,
  RefreshCw,
  Send,
  TerminalSquare,
} from 'lucide-react';

import {
  createAgentSession,
  getAgentCapabilities,
  listAgentRunners,
  listAgentSessionEvents,
  listAgentSessions,
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
  if (event.stream === 'tool') {
    return event.payload?.tool || event.payload?.id || 'Tool';
  }
  if (event.stream === 'control') {
    switch (event.type) {
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
  if (kind === 'error' || event.stream === 'control' || event.type === 'error') {
    return 'error';
  }
  if (kind === 'tool_call' || kind === 'tool_result' || event.stream === 'tool') {
    return 'tool';
  }
  if (event.stream === 'status') {
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
  if (stream === 'control' && type === 'error') return 'error';
  if (stream === 'control') return 'control';
  return 'event';
}

function isThinkingEvent(event) {
  return eventKind(event) === 'thinking';
}

function isModelResponseDelta(event) {
  return eventKind(event) === 'model_response' && event.type === 'output_delta';
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
    } else if (event.stream === 'control' && event.type === 'error') {
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

  for (const event of events) {
    if (pendingInputSeq > 0 && event.seq > pendingInputSeq && (isThinkingEvent(event) || isModelResponseDelta(event))) {
      continue;
    }
    if (isThinkingEvent(event)) {
      appendThinking(event);
      continue;
    }
    const message = conversationMessageFromEvent(event, agentLabel);
    if (message?.text) {
      flushFolded();
      if (message.role === 'user') {
        thinkingItem = null;
      }
      items.push({
        kind: 'message',
        key: message.key,
        message,
      });
      if (message.role !== 'user') {
        thinkingItem = null;
      }
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
  return session?.availability === LOCAL_CONVERSATION_AVAILABILITY;
}

function isConversationCloudOnly(session) {
  return session?.availability === CLOUD_ONLY_CONVERSATION_AVAILABILITY;
}

function conversationAvailabilityLabel(session) {
  switch (session?.availability) {
    case LOCAL_CONVERSATION_AVAILABILITY:
      return 'Local';
    case PENDING_LOCAL_CONVERSATION_AVAILABILITY:
      return 'Preparing local checkout';
    case CLOUD_ONLY_CONVERSATION_AVAILABILITY:
      return 'Cloud only';
    case 'runner_offline':
      return 'Runner offline';
    case 'failed':
      return 'Failed';
    default:
      return 'Unknown local copy';
  }
}

function conversationAvailabilityMessage(session) {
  switch (session?.availability) {
    case PENDING_LOCAL_CONVERSATION_AVAILABILITY:
      return 'Waiting for this runner to create the local checkout for the conversation.';
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
  switch (session?.availability) {
    case LOCAL_CONVERSATION_AVAILABILITY:
      return 0;
    case PENDING_LOCAL_CONVERSATION_AVAILABILITY:
      return 1;
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
  const [sessionsError, setSessionsError] = useState('');
  const [eventsError, setEventsError] = useState('');
  const [createError, setCreateError] = useState('');
  const [inputError, setInputError] = useState('');
  const [runnerActionError, setRunnerActionError] = useState('');
  const [runnersError, setRunnersError] = useState('');
  const [capabilities, setCapabilities] = useState(null);
  const [sessionsSidebarOpen, setSessionsSidebarOpen] = useState(true);
  const [sessionsSidebarDismissing, setSessionsSidebarDismissing] = useState(false);
  const [sessionsSidebarViewportSynced, setSessionsSidebarViewportSynced] = useState(false);
  const [agentsSidebarWidth, setAgentsSidebarWidth] = useState(readAgentsSidebarWidth);
  const [agentsSidebarResizing, setAgentsSidebarResizing] = useState(false);

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
  const localConversationCount = useMemo(
    () => runnerSessions.filter(isConversationLocal).length,
    [runnerSessions],
  );
  const cloudOnlyConversationCount = useMemo(
    () => runnerSessions.filter(isConversationCloudOnly).length,
    [runnerSessions],
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
  const runningAgentCountLabel = onlineRunners.length === 1 ? '1 running agent' : `${onlineRunners.length} running agents`;
  const sessionCountLabel = [
    localConversationCount === 1 ? '1 local conversation' : `${localConversationCount} local conversations`,
    cloudOnlyConversationCount > 0 ? `${cloudOnlyConversationCount} cloud-only` : '',
  ].filter(Boolean).join(' · ');
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

  useEffect(() => {
    if (!selectedSessionId) {
      loadSelectedEvents();
      return undefined;
    }

    let active = true;
    const pollIntervalMs = assistantStreaming ? 1000 : 5000;
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
  }, [assistantStreaming, loadSelectedEvents, selectedSessionId]);

  useEffect(() => {
    setRunnerActionError('');
    setEvents([]);
    setEventsError('');
  }, [selectedSessionId]);

  useEffect(() => {
    setAgentInfoOpen(false);
    setRunnerActionError('');
  }, [selectedRunnerId]);

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
          <div className="slice-agents-sidebar-header">
            <div>
              <h2>Running agents</h2>
              <span>{runningAgentCountLabel} · {sessionCountLabel}</span>
            </div>
            <div className="slice-agents-sidebar-actions">
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
                className="slice-agents-icon-button"
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
          </div>

          <section className="slice-agents-panel slice-agents-running-agents" data-testid="slice-agents-runners">
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
                  const runnerSessions = sessionsByRunnerId.get(runner.runnerId) || [];
                  const runnerLocalCount = runnerSessions.filter(isConversationLocal).length;
                  const runnerCloudOnlyCount = runnerSessions.filter(isConversationCloudOnly).length;
                  const runnerSessionLabel = [
                    runnerLocalCount === 1 ? '1 local' : `${runnerLocalCount} local`,
                    runnerCloudOnlyCount > 0 ? `${runnerCloudOnlyCount} cloud-only` : '',
                  ].filter(Boolean).join(' · ');
                  return (
                    <Button
                      key={runner.runnerId}
                      type="button"
                      variant="ghost"
                      role="tab"
                      className={`slice-agents-runner-tab${isSelected ? ' active' : ''}`}
                      onClick={() => handleRunnerSelect(runner.runnerId)}
                      aria-selected={isSelected}
                      title={runner.workspaceRoot || runner.runnerId}
                      data-testid="slice-agents-runner"
                    >
                      <span className="slice-agents-runner-tab-icon" aria-hidden="true">
                        <TerminalSquare size={15} />
                      </span>
                      <span className="slice-agents-runner-tab-main">
                        <span className="slice-agents-runner-tab-title">
                          <span className="slice-agents-runner-tab-status" aria-hidden="true" />
                          <span>{runner.agentType || 'agent'} · {runner.hostName || 'local'}</span>
                        </span>
                        <span className="slice-agents-runner-tab-meta">
                          {runner.status || 'unknown'} · {runnerSessionLabel}
                        </span>
                      </span>
                    </Button>
                  );
                })}
              </div>
            )}
            {selectedRunner && (
              <section className="slice-agents-runner-card" data-testid="slice-agents-runner-card">
                <div className="slice-agents-runner-card-header">
                  <div>
                    <h3>Agent environment</h3>
                    <span>{runningAgentSummary}</span>
                  </div>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="slice-agents-icon-button"
                    onClick={() => setAgentInfoOpen((open) => !open)}
                    aria-label="Inspect running agent"
                    aria-expanded={agentInfoOpen}
                    title="Inspect running agent"
                    data-testid="slice-agents-info"
                  >
                    <Info size={15} aria-hidden="true" />
                  </Button>
                </div>
                {runnerRunningDir && (
                  <div className="slice-agents-runner-dir" title={runnerRunningDir}>
                    {runnerRunningDir}
                  </div>
                )}
                {agentInfoOpen && (
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
                )}
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
                {runnerActionError && <div className="panel-error">{runnerActionError}</div>}
              </section>
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
                <h3>Conversations</h3>
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
            <>
              <div className="slice-agents-conversation-header">
                <div>
                  <h1>Conversation</h1>
                  <span>{conversationAvailabilityLabel(selectedSession)} · {selectedSession.agentType || 'agent'} · {selectedSession.sessionId}</span>
                </div>
              </div>
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
            </>
          )}
        </main>
      </div>
    </section>
  );
}
