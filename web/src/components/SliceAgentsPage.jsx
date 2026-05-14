import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Bot,
  PanelLeftOpen,
  PanelRightClose,
  PanelRightOpen,
} from 'lucide-react';

import {
  requestAgentSessionChangesetExport,
  requestAgentSessionLocalChanges,
} from '../api/agents.js';
import {
  AGENTS_SIDEBAR_MAX_WIDTH,
  AGENTS_SIDEBAR_MIN_WIDTH,
  LOCAL_CHANGES_REQUEST_TIMEOUT_MS,
} from '../features/agents/agentConstants.js';
import {
  buildConversationItems,
  latestChangesetExportEvent,
  latestChangesetExportFailureEvent,
  latestEventSeq,
  latestLocalChangesEvent,
  latestLocalChangesFailureEvent,
  latestRunnerState,
  payloadRequestId,
} from '../features/agents/agentEvents.js';
import {
  buildRunningAgentInfoRows,
  conversationAvailabilityLabel,
  formatAgentTimestamp,
  infoValue,
  isConversationCloudOnly,
  isConversationLocal,
  runnerStateValue,
} from '../features/agents/agentModels.js';
import {
  normalizeLocalChangesPayload,
} from '../features/agents/agentLocalChanges.js';
import { useAgentSessionActions } from '../features/agents/useAgentSessionActions.js';
import { useAgentSessionsData } from '../features/agents/useAgentSessionsData.js';
import { useAgentSessionsSidebar } from '../features/agents/useAgentSessionsSidebar.js';
import { getSliceDisplayName } from '../utils/slices.js';
import SliceDetailNav from './SliceDetailNav.jsx';
import SliceAgentsConversationThread from './agents/SliceAgentsConversationThread.jsx';
import SliceAgentsLocalChangesPanel from './agents/SliceAgentsLocalChangesPanel.jsx';
import SliceAgentsRunnerInfoDialog from './agents/SliceAgentsRunnerInfoDialog.jsx';
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
  const [agentInfoOpen, setAgentInfoOpen] = useState(false);
  const [localChangesRequesting, setLocalChangesRequesting] = useState(false);
  const [exportingChangeset, setExportingChangeset] = useState(false);
  const [pendingLocalChangesRequestId, setPendingLocalChangesRequestId] = useState('');
  const [pendingLocalChangesRequestedAt, setPendingLocalChangesRequestedAt] = useState(0);
  const [pendingChangesetExportRequestId, setPendingChangesetExportRequestId] = useState('');
  const [changesetMessage, setChangesetMessage] = useState('');
  const [localChangesError, setLocalChangesError] = useState('');
  const [localChangesPanelOpen, setLocalChangesPanelOpen] = useState(true);
  const autoLocalChangesSessionRef = useRef('');
  const autoLocalChangesOutputSeqRef = useRef(0);
  const localChangesBusy = localChangesRequesting || Boolean(pendingLocalChangesRequestId);
  const changesetExportBusy = exportingChangeset || Boolean(pendingChangesetExportRequestId);
  const {
    assistantStreaming,
    defaultAgentType,
    events,
    eventsError,
    eventsLoading,
    liveStreamState,
    loadRunners,
    loadSelectedEvents,
    loadSessions,
    onlineRunners,
    runnerSessions,
    runners,
    runnersError,
    runnersLoading,
    selectRunnerId,
    selectSessionForRunner,
    selectSessionId,
    selectedRunner,
    selectedRunnerId,
    selectedRunnerSessions,
    selectedSession,
    selectedSessionId,
    sessionsError,
    sessionsLoading,
  } = useAgentSessionsData({
    changesetExportBusy,
    localChangesBusy,
    onSelectSession,
    routeSessionId,
    sliceId,
  });
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
  const canCreateSession = Boolean(sliceId && selectedRunner?.runnerId);
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
  const {
    createError,
    creatingSession,
    handleCreateSession,
    handleSendInput,
    handleUpgradeRestartRunner,
    inputError,
    inputText,
    runnerActionError,
    runnerActionLoading,
    sendingInput,
    setInputText,
  } = useAgentSessionActions({
    canCreateSession,
    canRestartRunner,
    canSendInput,
    defaultAgentType,
    loadSelectedEvents,
    loadSessions,
    selectSessionId,
    selectedRunner,
    selectedRunnerId,
    selectedSessionId,
    sliceId,
  });
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
  const localChangesLoading = localChangesBusy;
  const changesetExportLoading = changesetExportBusy;
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

  const handleSessionSelect = useCallback((sessionId) => {
    selectSessionForRunner(sessionId);
    closeSessionsSidebarForMobile();
  }, [closeSessionsSidebarForMobile, selectSessionForRunner]);

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
    setLocalChangesError('');
    setPendingLocalChangesRequestId('');
    setPendingLocalChangesRequestedAt(0);
    setPendingChangesetExportRequestId('');
    setChangesetMessage('');
    autoLocalChangesOutputSeqRef.current = 0;
  }, [selectedSessionId]);

  useEffect(() => {
    setAgentInfoOpen(false);
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

  const localChangesPanelAvailable = Boolean(selectedSession && isConversationLocal(selectedSession));
  const localChangesPanelVisible = Boolean(localChangesPanelAvailable && localChangesPanelOpen);

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
            selectRunnerId(runnerId);
            setAgentInfoOpen(true);
          }}
          onRefresh={() => {
            loadRunners({ keepSelection: true });
            loadSessions({ keepSelection: true });
          }}
          onRunnerSelect={selectRunnerId}
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
                  <SliceAgentsLocalChangesPanel
                    assistantStreaming={assistantStreaming}
                    canExportChangeset={canExportChangeset}
                    canSendInput={canSendInput}
                    changesetExportLoading={changesetExportLoading}
                    changesetMessage={changesetMessage}
                    displayError={localChangesDisplayError}
                    hasDirtyFiles={hasDirtyFiles}
                    latestExportedChangesetId={latestExportedChangesetId}
                    localChanges={localChanges}
                    localChangesLoading={localChangesLoading}
                    onChangesetMessageChange={setChangesetMessage}
                    onExportChangeset={handleExportChangeset}
                    onHide={() => setLocalChangesPanelOpen(false)}
                    onRefresh={() => requestLocalChanges()}
                  />
                </aside>
              )}
            </div>
          )}
        </main>
      </div>
      {agentInfoOpen && selectedRunner && (
        <SliceAgentsRunnerInfoDialog
          canRestartRunner={canRestartRunner}
          error={runnerActionError}
          infoRows={runningAgentInfoRows}
          onClose={() => setAgentInfoOpen(false)}
          onUpgradeRestart={handleUpgradeRestartRunner}
          runnerActionLoading={runnerActionLoading}
          runnerRunningDir={runnerRunningDir}
          summary={runningAgentSummary}
        />
      )}
    </section>
  );
}
