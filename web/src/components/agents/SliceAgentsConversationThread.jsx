import {
  CircleAlert,
  Send,
  TerminalSquare,
} from 'lucide-react';

import {
  eventBody,
  eventTitle,
  eventTone,
  renderConversationMarkdown,
} from '../../features/agents/agentEvents.js';
import {
  conversationAvailabilityMessage,
  formatAgentTimestamp,
  isConversationLocal,
} from '../../features/agents/agentModels.js';
import { Button } from '../ui/button.jsx';

export default function SliceAgentsConversationThread({
  canSendInput,
  conversationItems,
  events,
  eventsError,
  eventsLoading,
  inputError,
  inputText,
  onInputChange,
  onSendInput,
  selectedSession,
  sendingInput,
}) {
  return (
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
      <form className="slice-agents-input-form" onSubmit={onSendInput}>
        <input
          className="slice-agents-input"
          data-testid="slice-agents-input"
          value={inputText}
          onChange={(event) => onInputChange(event.target.value)}
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
  );
}
