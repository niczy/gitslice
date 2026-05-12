import { useCallback, useEffect, useMemo, useState } from 'react';
import { Bot, CircleAlert, Plus, RefreshCw, TerminalSquare } from 'lucide-react';

import {
  createAgentSession,
  getAgentCapabilities,
  listAgentSessionEvents,
  listAgentSessions,
} from '../utils/api.js';
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
  if (event.stream === 'tool') {
    return event.payload?.tool || event.payload?.id || 'Tool';
  }
  if (event.stream === 'control') {
    return event.payload?.code || 'Control';
  }
  return event.stream || 'Agent';
}

function eventBody(event) {
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

export default function SliceAgentsPage({
  sliceId,
  slices,
  publicApiBaseUrl = '',
  onOpenCode,
  onOpenCommits,
  onOpenChangesets,
}) {
  const [sessions, setSessions] = useState([]);
  const [selectedSessionId, setSelectedSessionId] = useState('');
  const [events, setEvents] = useState([]);
  const [sessionsLoading, setSessionsLoading] = useState(false);
  const [eventsLoading, setEventsLoading] = useState(false);
  const [creatingSession, setCreatingSession] = useState(false);
  const [sessionsError, setSessionsError] = useState('');
  const [eventsError, setEventsError] = useState('');
  const [createError, setCreateError] = useState('');
  const [capabilities, setCapabilities] = useState(null);

  const currentSlice = useMemo(() => (
    (slices || []).find((slice) => slice.slice_id === sliceId) || null
  ), [sliceId, slices]);
  const sliceLabel = getSliceDisplayName(currentSlice?.name || sliceId || 'Slice');
  const sliceEnvironment = String(currentSlice?.environment || '').trim();
  const defaultAgentType = capabilities?.defaultAgentType || capabilities?.default_agent_type || '';
  const canCreateSession = Boolean(sliceId && sliceEnvironment);
  const selectedSession = sessions.find((session) => session.sessionId === selectedSessionId) || null;

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
  }, [sliceId]);

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
    loadSessions();
  }, [loadSessions]);

  useEffect(() => {
    if (!selectedSessionId) {
      setEvents([]);
      setEventsError('');
      setEventsLoading(false);
      return undefined;
    }

    let active = true;
    const loadEvents = async () => {
      setEventsLoading(true);
      setEventsError('');
      try {
        const payload = await listAgentSessionEvents(selectedSessionId, { sinceSeq: 0, limit: 200 });
        if (!active) return;
        setEvents((payload?.events || []).map(normalizeEvent));
      } catch (err) {
        if (!active) return;
        setEvents([]);
        setEventsError(err?.message || 'Unable to load agent conversation.');
      } finally {
        if (active) setEventsLoading(false);
      }
    };

    loadEvents();
    const intervalId = window.setInterval(loadEvents, 5000);
    return () => {
      active = false;
      window.clearInterval(intervalId);
    };
  }, [selectedSessionId]);

  const handleCreateSession = async () => {
    if (!canCreateSession || creatingSession) {
      return;
    }
    setCreatingSession(true);
    setCreateError('');
    try {
      const created = normalizeSession(await createAgentSession(sliceId, {
        environment: sliceEnvironment,
        agentType: defaultAgentType,
      }));
      await loadSessions({ keepSelection: true });
      setSelectedSessionId(created.sessionId);
    } catch (err) {
      setCreateError(err?.message || 'Unable to create agent session.');
    } finally {
      setCreatingSession(false);
    }
  };

  return (
    <section className="slice-agents-page" data-testid="slice-agents-page">
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
        <aside className="slice-agents-sidebar" aria-label="Agent sessions">
          <div className="slice-agents-sidebar-header">
            <div>
              <h2>Sessions</h2>
              <span>{sliceEnvironment || 'No remote agent connected'}</span>
            </div>
            <div className="slice-agents-sidebar-actions">
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="slice-agents-icon-button"
                onClick={() => loadSessions({ keepSelection: true })}
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
                title={canCreateSession ? 'New session' : 'Start a remote agent first'}
                data-testid="slice-agents-new-session"
              >
                <Plus size={15} aria-hidden="true" />
                {creatingSession ? 'Starting' : 'New'}
              </Button>
            </div>
          </div>

          {!canCreateSession && (
            <div className="slice-agents-connection-note" data-testid="slice-agents-connection-note">
              <CircleAlert size={15} aria-hidden="true" />
              <span>Start a remote agent to create sessions.</span>
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
                      onClick={() => setSelectedSessionId(session.sessionId)}
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
        </aside>

        <main className="slice-agents-conversation" data-testid="slice-agents-conversation">
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
                <span className={`slice-agents-state slice-agents-state--${selectedSession.state || 'unknown'}`}>
                  {selectedSession.state || 'unknown'}
                </span>
              </div>
              {eventsError && <div className="panel-error">{eventsError}</div>}
              {eventsLoading && events.length === 0 && <div className="panel-empty">Loading conversation...</div>}
              {!eventsLoading && !eventsError && events.length === 0 && (
                <div className="slice-agents-empty-detail">
                  <TerminalSquare size={22} aria-hidden="true" />
                  <span>No conversation events yet.</span>
                </div>
              )}
              {events.length > 0 && (
                <ol className="slice-agents-event-list">
                  {events.map((event) => (
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
              )}
            </>
          )}
        </main>
      </div>
    </section>
  );
}
