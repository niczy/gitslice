import {
  Bot,
  PanelLeftOpen,
  PanelRightClose,
  PanelRightOpen,
} from 'lucide-react';

import { Button } from '../ui/button.jsx';
import SliceAgentsConversationThread from './SliceAgentsConversationThread.jsx';
import SliceAgentsLocalChangesPanel from './SliceAgentsLocalChangesPanel.jsx';

export default function SliceAgentsConversationPanel({
  assistantStreaming,
  canExportChangeset,
  canSendInput,
  changesetExportLoading,
  changesetMessage,
  checkoutFailure,
  conversationItems,
  events,
  eventsError,
  eventsLoading,
  hasDirtyFiles,
  inputError,
  inputText,
  latestExportedChangesetId,
  localChanges,
  localChangesDisplayError,
  localChangesLoading,
  localChangesPanelAvailable,
  localChangesPanelOpen,
  localChangesPanelVisible,
  onChangesetMessageChange,
  onExportChangeset,
  onHideLocalChanges,
  onInputChange,
  onOpenSessionsSidebar,
  onRefreshLocalChanges,
  onSendInput,
  onToggleLocalChanges,
  selectedSession,
  selectedSessionIsLocal,
  selectedSessionSubtitle,
  sendingInput,
}) {
  return (
    <main className="slice-agents-conversation" data-testid="slice-agents-conversation">
      <div className="slice-agents-mobile-toolbar">
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="slice-agents-icon-button"
          onClick={onOpenSessionsSidebar}
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
                onClick={onToggleLocalChanges}
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
              checkoutFailure={checkoutFailure}
              conversationItems={conversationItems}
              events={events}
              eventsError={eventsError}
              eventsLoading={eventsLoading}
              inputError={inputError}
              inputText={inputText}
              onInputChange={onInputChange}
              onSendInput={onSendInput}
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
                onChangesetMessageChange={onChangesetMessageChange}
                onExportChangeset={onExportChangeset}
                onHide={onHideLocalChanges}
                onRefresh={onRefreshLocalChanges}
              />
            </aside>
          )}
        </div>
      )}
    </main>
  );
}
