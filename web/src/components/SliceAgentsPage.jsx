import { useCallback, useEffect, useState } from 'react';
import {
  Bot,
  PanelLeftOpen,
  PanelRightClose,
  PanelRightOpen,
} from 'lucide-react';

import {
  AGENTS_SIDEBAR_MAX_WIDTH,
  AGENTS_SIDEBAR_MIN_WIDTH,
} from '../features/agents/agentConstants.js';
import { useAgentLocalChanges } from '../features/agents/useAgentLocalChanges.js';
import { useAgentPageViewModel } from '../features/agents/useAgentPageViewModel.js';
import { useAgentSessionActions } from '../features/agents/useAgentSessionActions.js';
import { useAgentSessionsData } from '../features/agents/useAgentSessionsData.js';
import { useAgentSessionsSidebar } from '../features/agents/useAgentSessionsSidebar.js';
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
  const [localChangesPanelOpen, setLocalChangesPanelOpen] = useState(true);
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
    setEventPollingBusy,
  } = useAgentSessionsData({
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

  const {
    canCreateSession,
    canRestartRunner,
    canSendInput,
    conversationItems,
    currentSlice,
    localChangesPanelAvailable,
    localChangesPanelVisible,
    runningAgentInfoRows,
    runningAgentSummary,
    runnerRunningDir,
    selectedRunnerConversationCountLabel,
    selectedSessionIsLocal,
    selectedSessionSubtitle,
    showAgentSessionDocsLink,
    sliceLabel,
  } = useAgentPageViewModel({
    events,
    liveStreamState,
    localChangesPanelOpen,
    runnerSessions,
    selectedRunner,
    selectedRunnerSessions,
    selectedSession,
    selectedSessionId,
    sessionsError,
    sliceId,
    slices,
  });
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
  const {
    canExportChangeset,
    changesetExportLoading,
    changesetMessage,
    handleExportChangeset,
    hasDirtyFiles,
    latestExportedChangesetId,
    localChanges,
    localChangesDisplayError,
    localChangesLoading,
    requestLocalChanges,
    setChangesetMessage,
  } = useAgentLocalChanges({
    assistantStreaming,
    canSendInput,
    events,
    loadSelectedEvents,
    selectedSession,
    selectedSessionId,
    setEventPollingBusy,
  });
  const handleSessionSelect = useCallback((sessionId) => {
    selectSessionForRunner(sessionId);
    closeSessionsSidebarForMobile();
  }, [closeSessionsSidebarForMobile, selectSessionForRunner]);

  useEffect(() => {
    setAgentInfoOpen(false);
  }, [selectedRunnerId]);

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
                  <span>{selectedSessionSubtitle}</span>
                </div>
                {selectedSessionIsLocal && (
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
