import { useCallback, useEffect, useLayoutEffect, useMemo, useState } from 'react';
import {
  Bot,
  CircleAlert,
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
import { renderMarkdownHtml } from '../utils/markdown.js';
import { getSliceDisplayName } from '../utils/slices.js';
import SliceDetailNav from './SliceDetailNav.jsx';
import { Button } from './ui/button.jsx';

function normalizeSession(session) {
  return {
    sessionId: session?.sessionId ?? session?.session_id ?? '',
    sliceId: session?.sliceId ?? session?.slice_id ?? '',
    state: session?.state ?? '',
    createdAt: session?.createdAt ?? session?.created_at ?? '',
    lastActivityAt: session?.lastActivityAt ?? session?.last_activity_at ?? '',
    environment: session?.environment ?? '',
    agentType: session?.agentType ?? session?.agent_type ?? '',
    provider: session?.provider ?? '',
    runnerId: session?.runnerId ?? session?.runner_id ?? '',
  };
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
    payload: parseEventPayload(event?.payload),
  };
}

function parseEventPayload(value) {
  if (!value) {
    return {};
  }
  if (typeof value === 'object') {
    return value;
  }
  if (typeof value !== 'string') {
    return {};
  }
  const candidates = [value];
  try {
    candidates.push(atob(value));
  } catch {
    // Some local adapters already return JSON strings.
  }
  for (const candidate of candidates) {
    try {
      const parsed = JSON.parse(candidate);
      return parsed && typeof parsed === 'object' ? parsed : { text: String(parsed) };
    } catch {
      // Try the next representation.
    }
  }
  return { text: value };
}

function shortSessionId(sessionId) {
  return sessionId ? sessionId.replace(/^sess_?/, '').slice(0, 12) : 'unknown';
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
    return `State ${event.payload?.state || 'changed'}`;
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
  if (event.stream === 'control' || event.type === 'error') {
    return 'error';
  }
  if (event.stream === 'tool') {
    return 'tool';
  }
  if (event.stream === 'status') {
    return 'status';
  }
  return 'agent';
}

const ACTIVE_SESSION_STATES = new Set(['creating', 'starting', 'running', 'idle', 'stopping']);
const AGENT_EVENTS_PAGE_SIZE = 500;
const AGENT_EVENTS_MAX = 5000;
const SESSIONS_SIDEBAR_MOBILE_MAX_WIDTH = 900;
const useIsomorphicLayoutEffect = typeof window === 'undefined' ? useEffect : useLayoutEffect;

function payloadText(payload) {
  return payload?.text
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

function conversationMessageFromEvent(event) {
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
      label: exitCode === 0 ? 'Codex' : 'Codex error',
      ts: event.ts,
      text: payloadText(event.payload),
      failed: exitCode !== 0,
    };
  }
  return null;
}

function isAssistantResponseStreaming(events, session) {
  if (!session || !ACTIVE_SESSION_STATES.has(session.state)) {
    return false;
  }

  let pendingInput = false;
  for (const event of events) {
    if (event.stream === 'agent' && event.type === 'input' && payloadText(event.payload).trim()) {
      pendingInput = true;
    } else if (event.stream === 'agent' && event.type === 'output_final') {
      pendingInput = false;
    } else if (event.stream === 'control' && event.type === 'error') {
      pendingInput = false;
    } else if (event.stream === 'status' && event.type === 'state' && !ACTIVE_SESSION_STATES.has(event.payload?.state)) {
      pendingInput = false;
    }
  }

  return pendingInput;
}

function buildConversationItems(events, assistantStreaming = false) {
  const items = [];
  let folded = [];

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

  for (const event of events) {
    const message = conversationMessageFromEvent(event);
    if (message?.text) {
      flushFolded();
      items.push({
        kind: 'message',
        key: message.key,
        message,
      });
    } else {
      folded.push(event);
    }
  }
  flushFolded();

  if (assistantStreaming) {
    items.push({
      kind: 'streaming',
      key: 'assistant-streaming',
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

function buildAgentInfoRows(session, runnerState) {
  if (!session) {
    return [];
  }
  const rows = [
    ['State', session.state],
    ['Session', session.sessionId],
    ['Runner', session.runnerId],
    ['Provider', session.provider],
    ['Agent', session.agentType],
    ['Host', runnerState.host_name || runnerState.hostName],
    ['PID', runnerState.pid],
    ['Workspace', runnerState.workspace_root || runnerState.workspaceRoot],
    ['Running directory', runnerState.running_dir || runnerState.runningDir || runnerState.checkout_dir || runnerState.checkoutDir],
    ['Command', runnerState.command],
    ['Codex mode', runnerState.codex_mode || runnerState.codexMode],
    ['Attached', runnerState.attached_at || runnerState.attachedAt],
    ['Last activity', session.lastActivityAt],
    ['Created', session.createdAt],
  ];
  return rows
    .map(([label, value]) => [label, infoValue(value)])
    .filter(([, value]) => value);
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

  const currentSlice = useMemo(() => (
    (slices || []).find((slice) => slice.slice_id === sliceId) || null
  ), [sliceId, slices]);
  const sliceLabel = getSliceDisplayName(currentSlice?.name || sliceId || 'Slice');
  const normalizedRouteSessionId = String(routeSessionId || '').trim();
  const defaultAgentType = capabilities?.defaultAgentType || capabilities?.default_agent_type || '';
  const onlineRunners = useMemo(() => runners.filter((runner) => runner.status === 'online'), [runners]);
  const selectedRunner = onlineRunners.find((runner) => runner.runnerId === selectedRunnerId) || onlineRunners[0] || null;
  const canCreateSession = Boolean(sliceId && selectedRunner?.runnerId);
  const selectedSession = sessions.find((session) => session.sessionId === selectedSessionId) || null;
  const runnerState = useMemo(() => latestRunnerState(events), [events]);
  const agentInfoRows = useMemo(() => buildAgentInfoRows(selectedSession, runnerState), [selectedSession, runnerState]);
  const runnerHost = runnerStateValue(runnerState, 'host_name', 'hostName');
  const runnerPID = infoValue(runnerState?.pid);
  const runnerRunningDir = runnerStateValue(runnerState, 'running_dir', 'runningDir')
    || runnerStateValue(runnerState, 'checkout_dir', 'checkoutDir');
  const runnerAttached = Boolean(runnerHost || runnerPID || runnerRunningDir);
  const runnerSummary = runnerAttached
    ? [runnerHost, runnerPID ? `pid ${runnerPID}` : ''].filter(Boolean).join(' · ')
    : 'Waiting for local runner';
  const canRestartRunner = Boolean(
    selectedSession
    && selectedSession.provider === 'local'
    && ACTIVE_SESSION_STATES.has(selectedSession.state),
  );
  const assistantStreaming = useMemo(
    () => isAssistantResponseStreaming(events, selectedSession),
    [events, selectedSession],
  );
  const conversationItems = useMemo(
    () => buildConversationItems(events, assistantStreaming),
    [events, assistantStreaming],
  );
  const hasActiveSession = sessions.some((session) => ACTIVE_SESSION_STATES.has(session.state));
  const showAgentSessionDocsLink = !sessionsLoading && !sessionsError && !hasActiveSession;
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

  const handleSessionSelect = useCallback((sessionId) => {
    setSelectedSessionId(sessionId);
    if (onSelectSession) {
      onSelectSession(sessionId);
    } else {
      writeAgentSessionURL(sessionId);
    }
    if (typeof window !== 'undefined' && window.innerWidth <= SESSIONS_SIDEBAR_MOBILE_MAX_WIDTH) {
      closeSessionsSidebar();
    }
  }, [closeSessionsSidebar, onSelectSession]);

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
    if (!normalizedRouteSessionId) {
      return;
    }
    if (sessions.some((session) => session.sessionId === normalizedRouteSessionId)) {
      setSelectedSessionId(normalizedRouteSessionId);
    }
  }, [normalizedRouteSessionId, sessions]);

  const loadRunners = useCallback(async ({ keepSelection = true } = {}) => {
    setRunnersLoading(true);
    setRunnersError('');
    try {
      const nextRunners = (await listAgentRunners({ limit: 50 })).map(normalizeRunner);
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
      setRunnersError(err?.message || 'Unable to load agent runners.');
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
        return nextSessions[0]?.sessionId || '';
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
    const loadEvents = async () => {
      if (!active) return;
      await loadSelectedEvents();
    };

    loadEvents();
    const intervalId = window.setInterval(loadEvents, 5000);
    return () => {
      active = false;
      window.clearInterval(intervalId);
    };
  }, [loadSelectedEvents, selectedSessionId]);

  useEffect(() => {
    setAgentInfoOpen(false);
    setRunnerActionError('');
    setEvents([]);
    setEventsError('');
  }, [selectedSessionId]);

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
      setSelectedSessionId(created.sessionId);
      if (onSelectSession) {
        onSelectSession(created.sessionId);
      } else {
        writeAgentSessionURL(created.sessionId);
      }
    } catch (err) {
      setCreateError(err?.message || 'Unable to create agent session.');
    } finally {
      setCreatingSession(false);
    }
  };

  const handleSendInput = async (event) => {
    event.preventDefault();
    const text = inputText.trim();
    if (!selectedSessionId || !text || sendingInput) {
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

      <div className="slice-agents-layout">
        <div
          className={`slice-agents-sidebar-overlay${sessionsSidebarVisible ? ' visible' : ''}${sessionsSidebarDismissing ? ' dismissing' : ''}`}
          onClick={closeSessionsSidebar}
        />
        <aside
          className={`slice-agents-sidebar ${sessionsSidebarOpen ? 'open' : 'closed'}${sessionsSidebarDismissing ? ' dismissing' : ''}`}
          aria-label="Agent sessions"
        >
          <div className="slice-agents-sidebar-header">
            <div>
              <h2>Sessions</h2>
              <span>{selectedRunner ? `${selectedRunner.agentType || 'agent'} on ${selectedRunner.hostName || 'local runner'}` : 'No local runner online'}</span>
            </div>
            <div className="slice-agents-sidebar-actions">
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="slice-agents-icon-button slice-agents-sidebar-close"
                onClick={closeSessionsSidebar}
                aria-label="Close sessions"
                title="Close sessions"
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
                aria-label="Refresh agent sessions"
                title="Refresh"
              >
                <RefreshCw size={15} aria-hidden="true" />
              </Button>
              <Button
                type="button"
                variant="default"
                size="sm"
                className="slice-agents-new-button"
                onClick={handleCreateSession}
                disabled={!canCreateSession || creatingSession}
                title={canCreateSession ? 'New session' : 'Start a local runner first'}
                data-testid="slice-agents-new-session"
              >
                <Plus size={15} aria-hidden="true" />
                {creatingSession ? 'Starting' : 'New'}
              </Button>
            </div>
          </div>

          <section className="slice-agents-available-runners" data-testid="slice-agents-runners">
            <div className="slice-agents-section-label">Available runners</div>
            {runnersLoading && runners.length === 0 && <div className="panel-empty">Loading runners...</div>}
            {!runnersLoading && runnersError && <div className="panel-error">{runnersError}</div>}
            {!runnersLoading && !runnersError && onlineRunners.length === 0 && (
              <div className="panel-empty">No local runners online.</div>
            )}
            {onlineRunners.length > 0 && (
              <ul className="slice-agents-session-list">
                {onlineRunners.map((runner) => {
                  const isSelected = selectedRunner?.runnerId === runner.runnerId;
                  return (
                    <li key={runner.runnerId}>
                      <Button
                        type="button"
                        variant="ghost"
                        className={`slice-agents-session-row${isSelected ? ' active' : ''}`}
                        onClick={() => setSelectedRunnerId(runner.runnerId)}
                        aria-pressed={isSelected}
                        data-testid="slice-agents-runner"
                      >
                        <span className="slice-agents-session-icon" aria-hidden="true">
                          <TerminalSquare size={15} />
                        </span>
                        <span className="slice-agents-session-main">
                          <span className="slice-agents-session-title">
                            {runner.agentType || 'agent'} · {runner.hostName || 'local'}
                          </span>
                          <span className="slice-agents-session-meta">
                            {runner.status || 'unknown'} · {runner.workspaceRoot || runner.runnerId}
                          </span>
                        </span>
                      </Button>
                    </li>
                  );
                })}
              </ul>
            )}
          </section>

          {!canCreateSession && (
            <div className="slice-agents-connection-note" data-testid="slice-agents-connection-note">
              <CircleAlert size={15} aria-hidden="true" />
              <span>Start a local runner to create sessions.</span>
            </div>
          )}
          {showAgentSessionDocsLink && (
            <div className="slice-agents-docs-note" data-testid="slice-agents-docs-note">
              <CircleAlert size={15} aria-hidden="true" />
              <span>
                No active agent session. <a href="/docs#local-agent-sessions">Start a local runner with gs.</a>
              </span>
            </div>
          )}
          {createError && <div className="panel-error">{createError}</div>}
          {sessionsError && <div className="panel-error">{sessionsError}</div>}
          {sessionsLoading && sessions.length === 0 && <div className="panel-empty">Loading sessions...</div>}
          {!sessionsLoading && !sessionsError && sessions.length === 0 && (
            <div className="panel-empty">No agent sessions yet.</div>
          )}
          {sessions.length > 0 && (
            <ul className="slice-agents-session-list">
              {sessions.map((session) => {
                const isSelected = session.sessionId === selectedSessionId;
                return (
                  <li key={session.sessionId}>
                    <Button
                      type="button"
                      variant="ghost"
                      className={`slice-agents-session-row${isSelected ? ' active' : ''}`}
                      onClick={() => handleSessionSelect(session.sessionId)}
                      aria-pressed={isSelected}
                      data-testid="slice-agents-session"
                    >
                      <span className="slice-agents-session-icon" aria-hidden="true">
                        <Bot size={15} />
                      </span>
                      <span className="slice-agents-session-main">
                        <span className="slice-agents-session-title">
                          {session.agentType || 'agent'} · {shortSessionId(session.sessionId)}
                        </span>
                        <span className="slice-agents-session-meta">
                          {session.state || 'unknown'} · {formatAgentTimestamp(session.lastActivityAt || session.createdAt)}
                        </span>
                      </span>
                    </Button>
                  </li>
                );
              })}
            </ul>
          )}
          {selectedSession && (
            <section className="slice-agents-runner-card" data-testid="slice-agents-runner-card">
              <div className="slice-agents-runner-card-header">
                <div>
                  <h3>Local runner</h3>
                  <span>{runnerSummary}</span>
                </div>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="slice-agents-icon-button"
                  onClick={() => setAgentInfoOpen((open) => !open)}
                  aria-label="Inspect agent state"
                  aria-expanded={agentInfoOpen}
                  title="Inspect agent state"
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
                    {agentInfoRows.map(([label, value]) => (
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
                title={canRestartRunner ? 'Upgrade and restart local runner' : 'Select an active local session'}
                data-testid="slice-agents-upgrade-restart"
              >
                <RefreshCw size={15} aria-hidden="true" />
                {runnerActionLoading ? 'Requesting' : 'Upgrade & restart'}
              </Button>
              {runnerActionError && <div className="panel-error">{runnerActionError}</div>}
            </section>
          )}
        </aside>

        <main className="slice-agents-conversation" data-testid="slice-agents-conversation">
          <div className="slice-agents-mobile-toolbar">
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="slice-agents-icon-button"
              onClick={openSessionsSidebar}
              aria-label="Open sessions"
              title="Open sessions"
              data-testid="slice-agents-open-sessions"
            >
              <PanelLeftOpen size={16} aria-hidden="true" />
            </Button>
            <span>Sessions</span>
          </div>
          {!selectedSession && (
            <div className="slice-agents-empty-detail">
              <Bot size={22} aria-hidden="true" />
              <span>Select a session to view its conversation.</span>
            </div>
          )}
          {selectedSession && (
            <>
              <div className="slice-agents-conversation-header">
                <div>
                  <h1>{selectedSession.agentType || 'Agent'} session</h1>
                  <span>{selectedSession.sessionId}</span>
                </div>
                <div className="slice-agents-header-actions">
                  <span className={`slice-agents-state slice-agents-state--${selectedSession.state || 'unknown'}`}>
                    {selectedSession.state || 'unknown'}
                  </span>
                </div>
              </div>
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
                    ) : item.kind === 'streaming' ? (
                      <li
                        key={item.key}
                        className="slice-agents-message slice-agents-message--assistant slice-agents-message--streaming"
                        aria-live="polite"
                        data-testid="slice-agents-streaming"
                      >
                        <div className="slice-agents-message-header">
                          <span>Codex</span>
                          <span className="slice-agents-streaming-label">Responding</span>
                        </div>
                        <div className="slice-agents-streaming-dots" aria-label="Codex is responding">
                          <span />
                          <span />
                          <span />
                        </div>
                      </li>
                    ) : (
                      <li key={item.key} className="slice-agents-timeline-events">
                        <details className="slice-agents-debug-events">
                          <summary>System and tool events ({item.events.length})</summary>
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
                  placeholder="Message agent"
                  disabled={sendingInput}
                />
                <Button
                  type="submit"
                  variant="default"
                  size="icon"
                  className="slice-agents-send-button"
                  disabled={!inputText.trim() || sendingInput}
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
