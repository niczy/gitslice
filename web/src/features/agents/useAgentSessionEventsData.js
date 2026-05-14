import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import {
  listAgentSessionEvents,
  mintAgentSessionToken,
} from '../../api/agents.js';
import {
  AGENT_EVENTS_MAX,
  AGENT_EVENTS_PAGE_SIZE,
} from './agentConstants.js';
import {
  buildLiveStreamState,
  normalizeEvent,
} from './agentEvents.js';
import { isConversationLocal } from './agentModels.js';

function lastEventSeq(events) {
  return events.length > 0 ? events[events.length - 1].seq : 0;
}

function mergeConversationEvents(currentEvents, incomingEvents) {
  if (!Array.isArray(incomingEvents) || incomingEvents.length === 0) {
    return currentEvents;
  }
  const seen = new Set(currentEvents.map((event) => event.seq));
  const additions = incomingEvents
    .map(normalizeEvent)
    .filter((event) => event.seq > 0 && !seen.has(event.seq));
  if (additions.length === 0) {
    return currentEvents;
  }
  return [...currentEvents, ...additions]
    .sort((a, b) => a.seq - b.seq)
    .slice(-AGENT_EVENTS_MAX);
}

export function useAgentSessionEventsData({
  selectedSession,
  selectedSessionId,
}) {
  const [events, setEvents] = useState([]);
  const eventsRef = useRef([]);
  const lastEventSeqRef = useRef(0);
  const [eventsRealtimeConnected, setEventsRealtimeConnected] = useState(false);
  const [eventsLoading, setEventsLoading] = useState(false);
  const [eventsError, setEventsError] = useState('');
  const [eventPollingBusy, setEventPollingBusy] = useState(false);

  const liveStreamState = useMemo(
    () => buildLiveStreamState(events, selectedSession),
    [events, selectedSession],
  );
  const assistantStreaming = liveStreamState.active;

  const replaceConversationEvents = useCallback((nextEvents) => {
    const normalized = (nextEvents || []).map(normalizeEvent).slice(-AGENT_EVENTS_MAX);
    eventsRef.current = normalized;
    lastEventSeqRef.current = lastEventSeq(normalized);
    setEvents(normalized);
  }, []);

  const resetConversationEvents = useCallback(() => {
    eventsRef.current = [];
    lastEventSeqRef.current = 0;
    setEvents([]);
  }, []);

  const appendConversationEvents = useCallback((incomingEvents) => {
    setEvents((currentEvents) => {
      const merged = mergeConversationEvents(currentEvents, incomingEvents);
      if (merged === currentEvents) {
        return currentEvents;
      }
      eventsRef.current = merged;
      lastEventSeqRef.current = lastEventSeq(merged);
      return merged;
    });
  }, []);

  const loadSelectedEvents = useCallback(async ({ incremental = false } = {}) => {
    if (!selectedSessionId) {
      resetConversationEvents();
      setEventsError('');
      setEventsLoading(false);
      return;
    }

    const existingEvents = eventsRef.current;
    setEventsLoading(!incremental && existingEvents.length === 0);
    setEventsError('');
    try {
      let sinceSeq = incremental ? lastEventSeqRef.current : 0;
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
      if (incremental) {
        appendConversationEvents(nextEvents);
      } else {
        replaceConversationEvents(nextEvents);
      }
    } catch (err) {
      if (!incremental) {
        resetConversationEvents();
      }
      setEventsError(err?.message || 'Unable to load agent conversation.');
    } finally {
      setEventsLoading(false);
    }
  }, [appendConversationEvents, replaceConversationEvents, resetConversationEvents, selectedSessionId]);

  useEffect(() => {
    resetConversationEvents();
    setEventsError('');
  }, [resetConversationEvents, selectedSessionId]);

  useEffect(() => {
    if (!selectedSessionId || !selectedSession || !isConversationLocal(selectedSession) || typeof window === 'undefined') {
      setEventsRealtimeConnected(false);
      return undefined;
    }

    let closed = false;
    let socket = null;
    let reconnectTimer = 0;
    let reconnectDelay = 1000;

    const scheduleReconnect = () => {
      if (closed) return;
      reconnectTimer = window.setTimeout(connect, reconnectDelay);
      reconnectDelay = Math.min(reconnectDelay * 2, 5000);
    };

    const connect = async () => {
      if (closed) return;
      setEventsRealtimeConnected(false);
      try {
        const tokenInfo = await mintAgentSessionToken(selectedSessionId);
        if (closed) return;
        const url = new URL(tokenInfo.url || `/ws/sessions/${encodeURIComponent(selectedSessionId)}`, window.location.href);
        url.searchParams.set('token', tokenInfo.token || '');
        url.searchParams.set('lastSeq', String(lastEventSeqRef.current || 0));

        socket = new WebSocket(url.toString());
        socket.onopen = () => {
          reconnectDelay = 1000;
          setEventsRealtimeConnected(true);
        };
        socket.onmessage = (event) => {
          try {
            const frame = JSON.parse(event.data);
            if (frame?.stream && frame?.type) {
              appendConversationEvents([frame]);
            }
          } catch {
            // Ignore malformed frames; the fallback poller will catch up.
          }
        };
        socket.onerror = () => {
          socket?.close();
        };
        socket.onclose = () => {
          if (closed) return;
          setEventsRealtimeConnected(false);
          scheduleReconnect();
        };
      } catch {
        scheduleReconnect();
      }
    };

    connect();
    return () => {
      closed = true;
      setEventsRealtimeConnected(false);
      if (reconnectTimer) {
        window.clearTimeout(reconnectTimer);
      }
      if (socket && socket.readyState !== WebSocket.CLOSED) {
        socket.close();
      }
    };
  }, [appendConversationEvents, selectedSession, selectedSessionId]);

  useEffect(() => {
    if (!selectedSessionId) {
      loadSelectedEvents();
      return undefined;
    }
    if (typeof window === 'undefined') {
      loadSelectedEvents();
      return undefined;
    }

    let active = true;
    const pollIntervalMs = assistantStreaming || eventPollingBusy ? 1000 : 5000;
    const loadEvents = async () => {
      if (!active || eventsRealtimeConnected) return;
      await loadSelectedEvents({ incremental: true });
    };

    loadEvents();
    const intervalId = window.setInterval(loadEvents, pollIntervalMs);
    return () => {
      active = false;
      window.clearInterval(intervalId);
    };
  }, [assistantStreaming, eventPollingBusy, eventsRealtimeConnected, loadSelectedEvents, selectedSessionId]);

  return {
    assistantStreaming,
    events,
    eventsError,
    eventsLoading,
    liveStreamState,
    loadSelectedEvents,
    setEventPollingBusy,
  };
}
