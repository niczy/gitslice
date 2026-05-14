import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Bot,
  ExternalLink,
  FileDiff,
  GitPullRequest,
  PanelLeftOpen,
  PanelRightClose,
  PanelRightOpen,
  RefreshCw,
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
} from '../api/agents.js';
import {
  AGENT_EVENTS_MAX,
  AGENT_EVENTS_PAGE_SIZE,
  AGENTS_SIDEBAR_MAX_WIDTH,
  AGENTS_SIDEBAR_MIN_WIDTH,
  LOCAL_CHANGES_REQUEST_TIMEOUT_MS,
} from '../features/agents/agentConstants.js';
import {
  buildConversationItems,
  buildLiveStreamState,
  latestChangesetExportEvent,
  latestChangesetExportFailureEvent,
  latestEventSeq,
  latestLocalChangesEvent,
  latestLocalChangesFailureEvent,
  latestRunnerState,
  normalizeEvent,
  payloadRequestId,
} from '../features/agents/agentEvents.js';
import {
  writeAgentSessionURL,
} from '../features/agents/agentLayout.js';
import {
  buildRunningAgentInfoRows,
  conversationAvailabilityLabel,
  conversationAvailabilityRank,
  formatAgentTimestamp,
  infoValue,
  isConversationCloudOnly,
  isConversationLocal,
  normalizeRunner,
  normalizeSession,
  runnerStateValue,
  shortEntityId,
} from '../features/agents/agentModels.js';
import {
  changeStatusLabel,
  localChangesSummaryText,
  normalizeLocalChangesPayload,
} from '../features/agents/agentLocalChanges.js';
import { useAgentSessionsSidebar } from '../features/agents/useAgentSessionsSidebar.js';
import { getSliceDisplayName } from '../utils/slices.js';
import SliceDetailNav from './SliceDetailNav.jsx';
import SliceAgentsConversationThread from './agents/SliceAgentsConversationThread.jsx';
import SliceAgentsSidebar from './agents/SliceAgentsSidebar.jsx';
import { Button } from './ui/button.jsx';

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
  const [localChangesPanelOpen, setLocalChangesPanelOpen] = useState(true);
  const autoLocalChangesSessionRef = useRef('');
  const autoLocalChangesOutputSeqRef = useRef(0);
  const {
    agentsLayoutRef,
    agentsSidebarResizing,
    agentsSidebarWidth,
    closeSessionsSidebarForMobile,
    closeSessionsSidebar,
    handleAgentsSidebarResizeKeyDown,
    handleAgentsSidebarResizePointerDown,
    openSessionsSidebar,
    resetAgentsSidebarWidth,
    sessionsSidebarDismissing,
    sessionsSidebarOpen,
    sessionsSidebarViewportSynced,
    sessionsSidebarVisible,
  } = useAgentSessionsSidebar();

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
    closeSessionsSidebarForMobile();
  }, [closeSessionsSidebarForMobile, selectSessionId, selectedRunnerSessions]);

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
        <SliceAgentsSidebar
          agentInfoOpen={agentInfoOpen}
          canCreateSession={canCreateSession}
          createError={createError}
          creatingSession={creatingSession}
          onClose={closeSessionsSidebar}
          onCreateSession={handleCreateSession}
          onInspectRunner={(runnerId) => {
            handleRunnerSelect(runnerId);
            setAgentInfoOpen(true);
          }}
          onRefresh={() => {
            loadRunners({ keepSelection: true });
            loadSessions({ keepSelection: true });
          }}
          onRunnerSelect={handleRunnerSelect}
          onSessionSelect={handleSessionSelect}
          onlineRunners={onlineRunners}
          runners={runners}
          runnersError={runnersError}
          runnersLoading={runnersLoading}
          selectedRunner={selectedRunner}
          selectedRunnerConversationCountLabel={selectedRunnerConversationCountLabel}
          selectedRunnerSessions={selectedRunnerSessions}
          selectedSessionId={selectedSessionId}
          sessionsDismissing={sessionsSidebarDismissing}
          sessionsError={sessionsError}
          sessionsLoading={sessionsLoading}
          sessionsOpen={sessionsSidebarOpen}
          sessionsVisible={sessionsSidebarVisible}
          showAgentSessionDocsLink={showAgentSessionDocsLink}
        />

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
          onDoubleClick={resetAgentsSidebarWidth}
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
                <SliceAgentsConversationThread
                  canSendInput={canSendInput}
                  conversationItems={conversationItems}
                  events={events}
                  eventsError={eventsError}
                  eventsLoading={eventsLoading}
                  inputError={inputError}
                  inputText={inputText}
                  onInputChange={setInputText}
                  onSendInput={handleSendInput}
                  selectedSession={selectedSession}
                  sendingInput={sendingInput}
                />
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
