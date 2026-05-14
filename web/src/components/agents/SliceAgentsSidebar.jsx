import {
  Bot,
  CircleAlert,
  CloudOff,
  Info,
  PanelLeftClose,
  Plus,
  RefreshCw,
} from 'lucide-react';

import {
  conversationAvailabilityLabel,
  formatAgentTimestamp,
  isConversationCloudOnly,
  runnerDisplayName,
  shortSessionId,
} from '../../features/agents/agentModels.js';
import { Button } from '../ui/button.jsx';

export default function SliceAgentsSidebar({
  agentInfoOpen,
  canCreateSession,
  createError,
  creatingSession,
  onClose,
  onCreateSession,
  onInspectRunner,
  onRefresh,
  onRunnerSelect,
  onSessionSelect,
  onlineRunners,
  runners,
  runnersError,
  runnersLoading,
  selectedRunner,
  selectedRunnerConversationCountLabel,
  selectedRunnerSessions,
  selectedSessionId,
  sessionsDismissing,
  sessionsError,
  sessionsLoading,
  sessionsOpen,
  sessionsVisible,
  showAgentSessionDocsLink,
}) {
  return (
    <>
      <div
        className={`slice-agents-sidebar-overlay${sessionsVisible ? ' visible' : ''}${sessionsDismissing ? ' dismissing' : ''}`}
        onClick={onClose}
      />
      <aside
        className={`slice-agents-sidebar ${sessionsOpen ? 'open' : 'closed'}${sessionsDismissing ? ' dismissing' : ''}`}
        aria-label="Running agents and conversations"
      >
        <section className="slice-agents-panel slice-agents-running-agents" data-testid="slice-agents-runners">
          <div className="slice-agents-runner-toolbar">
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="slice-agents-icon-button slice-agents-sidebar-close"
              onClick={onClose}
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
              onClick={onRefresh}
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
                      onClick={() => onRunnerSelect(runner.runnerId)}
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
                      onClick={() => onInspectRunner(runner.runnerId)}
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
              onClick={onCreateSession}
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
                      onClick={() => onSessionSelect(session.sessionId)}
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
    </>
  );
}
