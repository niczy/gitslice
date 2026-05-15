import { useCallback, useEffect, useState } from 'react';

import { useAgentLocalChanges } from '../features/agents/useAgentLocalChanges.js';
import { useAgentPageViewModel } from '../features/agents/useAgentPageViewModel.js';
import { useAgentSessionActions } from '../features/agents/useAgentSessionActions.js';
import { useAgentSessionsData } from '../features/agents/useAgentSessionsData.js';
import { useAgentSessionsSidebar } from '../features/agents/useAgentSessionsSidebar.js';
import SliceDetailNav from './SliceDetailNav.jsx';
import SliceAgentsConversationPanel from './agents/SliceAgentsConversationPanel.jsx';
import SliceAgentsResizeHandle from './agents/SliceAgentsResizeHandle.jsx';
import SliceAgentsRunnerInfoDialog from './agents/SliceAgentsRunnerInfoDialog.jsx';
import SliceAgentsSidebar from './agents/SliceAgentsSidebar.jsx';

export default function SliceAgentsPage({
  sliceId,
  routeSessionId = '',
  slices,
  isAuthenticated = false,
  publicApiBaseUrl = '',
  onOpenCode,
  onOpenCommits,
  onOpenChangesets,
  onOpenSettings,
  onSelectSession,
}) {
  const [agentInfoOpen, setAgentInfoOpen] = useState(false);
  const [localChangesPanelOpen, setLocalChangesPanelOpen] = useState(true);
  const [selectedAgentType, setSelectedAgentType] = useState('');
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
    isAuthenticated,
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
    checkoutFailure,
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
    isAuthenticated,
    selectedRunner,
    selectedRunnerSessions,
    selectedSession,
    selectedSessionId,
    sessionsError,
    sliceId,
    slices,
  });
  const selectedRunnerSupportedAgentTypes = selectedRunner?.supportedAgentTypes?.length
    ? selectedRunner.supportedAgentTypes
    : [selectedRunner?.defaultAgentType || selectedRunner?.agentType || defaultAgentType].filter(Boolean);
  const selectedRunnerAgentTypesKey = selectedRunnerSupportedAgentTypes.join('|');
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
    selectedAgentType,
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
    latestExportedChangeset,
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
    localChangesPanelOpen,
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
    if (!selectedRunner) {
      setSelectedAgentType('');
      return;
    }
    const fallback = selectedRunner.defaultAgentType
      || defaultAgentType
      || selectedRunnerSupportedAgentTypes[0]
      || '';
    setSelectedAgentType((current) => (
      selectedRunnerSupportedAgentTypes.includes(current) ? current : fallback
    ));
  }, [defaultAgentType, selectedRunner, selectedRunnerAgentTypesKey]);

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
        onOpenSettings={onOpenSettings}
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
          onAgentTypeChange={setSelectedAgentType}
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
          selectedRunnerSupportedAgentTypes={selectedRunnerSupportedAgentTypes}
          selectedAgentType={selectedAgentType}
          selectedRunnerSessions={selectedRunnerSessions}
          selectedSessionId={selectedSessionId}
          sessionsDismissing={sessionsSidebarDismissing}
          sessionsError={sessionsError}
          sessionsLoading={sessionsLoading}
          sessionsOpen={sessionsSidebarOpen}
          sessionsVisible={sessionsSidebarVisible}
          showAgentSessionDocsLink={showAgentSessionDocsLink}
        />

        <SliceAgentsResizeHandle
          agentsSidebarWidth={agentsSidebarWidth}
          onDoubleClick={resetAgentsSidebarWidth}
          onKeyDown={handleAgentsSidebarResizeKeyDown}
          onPointerDown={handleAgentsSidebarResizePointerDown}
        />

        <SliceAgentsConversationPanel
          assistantStreaming={assistantStreaming}
          canExportChangeset={canExportChangeset}
          canSendInput={canSendInput}
          changesetExportLoading={changesetExportLoading}
          changesetMessage={changesetMessage}
          checkoutFailure={checkoutFailure}
          conversationItems={conversationItems}
          events={events}
          eventsError={eventsError}
          eventsLoading={eventsLoading}
          hasDirtyFiles={hasDirtyFiles}
          inputError={inputError}
          inputText={inputText}
          latestExportedChangeset={latestExportedChangeset}
          latestExportedChangesetId={latestExportedChangesetId}
          localChanges={localChanges}
          localChangesDisplayError={localChangesDisplayError}
          localChangesLoading={localChangesLoading}
          localChangesPanelAvailable={localChangesPanelAvailable}
          localChangesPanelOpen={localChangesPanelOpen}
          localChangesPanelVisible={localChangesPanelVisible}
          onChangesetMessageChange={setChangesetMessage}
          onExportChangeset={handleExportChangeset}
          onHideLocalChanges={() => setLocalChangesPanelOpen(false)}
          onInputChange={setInputText}
          onOpenSessionsSidebar={openSessionsSidebar}
          onRefreshLocalChanges={() => requestLocalChanges()}
          onSendInput={handleSendInput}
          onToggleLocalChanges={() => setLocalChangesPanelOpen((open) => !open)}
          selectedSession={selectedSession}
          selectedSessionIsLocal={selectedSessionIsLocal}
          selectedSessionSubtitle={selectedSessionSubtitle}
          sendingInput={sendingInput}
        />
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
