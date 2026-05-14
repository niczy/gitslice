import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import {
  getAgentCapabilities,
  listAgentRunners,
  listAgentSessionEvents,
  listAgentSessions,
  mintAgentSessionToken,
  waitForAgentRunnerUpdates,
} from '../../api/agents.js';
import {
  AGENT_EVENTS_MAX,
  AGENT_EVENTS_PAGE_SIZE,
} from './agentConstants.js';
import {
  buildLiveStreamState,
  normalizeEvent,
} from './agentEvents.js';
import { writeAgentSessionURL } from './agentLayout.js';
import {
  conversationAvailabilityRank,
  isConversationLocal,
  normalizeRunner,
  normalizeSession,
} from './agentModels.js';

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

const RUNNER_UPDATE_WAIT_MS = 25000;
const RUNNER_WATCH_RETRY_MS = 5000;

export function useAgentSessionsData({
  onSelectSession,
  routeSessionId = '',
  sliceId,
}) {
  const [sessions, setSessions] = useState([]);
  const [runners, setRunners] = useState([]);
  const [selectedSessionId, setSelectedSessionId] = useState('');
  const [selectedRunnerId, setSelectedRunnerId] = useState('');
  const [events, setEvents] = useState([]);
  const eventsRef = useRef([]);
  const lastEventSeqRef = useRef(0);
  const [eventsRealtimeConnected, setEventsRealtimeConnected] = useState(false);
  const [runnersLoading, setRunnersLoading] = useState(false);
  const [sessionsLoading, setSessionsLoading] = useState(false);
  const [eventsLoading, setEventsLoading] = useState(false);
  const [sessionsError, setSessionsError] = useState('');
  const [eventsError, setEventsError] = useState('');
  const [runnersError, setRunnersError] = useState('');
  const [capabilities, setCapabilities] = useState(null);
  const [eventPollingBusy, setEventPollingBusy] = useState(false);

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
  const selectedSession = selectedRunnerSessions.find((session) => session.sessionId === selectedSessionId) || null;
  const liveStreamState = useMemo(
    () => buildLiveStreamState(events, selectedSession),
    [events, selectedSession],
  );
  const assistantStreaming = liveStreamState.active;

  const selectSessionId = useCallback((sessionId, { runnerId = '' } = {}) => {
    if (runnerId) {
      setSelectedRunnerId(runnerId);
    }
    setSelectedSessionId(sessionId);
    if (onSelectSession) {
      onSelectSession(sessionId);
    } else {
      writeAgentSessionURL(sessionId);
    }
  }, [onSelectSession]);

  const selectRunnerId = useCallback((runnerId) => {
    setSelectedRunnerId(runnerId);
    const nextSessionId = (sessionsByRunnerId.get(runnerId) || [])[0]?.sessionId || '';
    selectSessionId(nextSessionId);
  }, [selectSessionId, sessionsByRunnerId]);

  const selectSessionForRunner = useCallback((sessionId) => {
    const nextSession = selectedRunnerSessions.find((session) => session.sessionId === sessionId);
    selectSessionId(sessionId, { runnerId: nextSession?.runnerId || '' });
  }, [selectSessionId, selectedRunnerSessions]);

  const loadRunners = useCallback(async ({ keepSelection = true, quiet = false } = {}) => {
    if (!quiet) {
      setRunnersLoading(true);
      setRunnersError('');
    }
    try {
      const nextRunners = (await listAgentRunners({ limit: 50, includeOffline: true })).map(normalizeRunner);
      setRunnersError('');
      setRunners(nextRunners);
      setSelectedRunnerId((current) => {
        if (keepSelection && current && nextRunners.some((runner) => runner.runnerId === current && runner.status === 'online')) {
          return current;
        }
        return nextRunners.find((runner) => runner.status === 'online')?.runnerId || '';
      });
    } catch (err) {
      if (quiet) {
        return;
      }
      setRunners([]);
      setSelectedRunnerId('');
      setRunnersError(err?.message || 'Unable to load running agents.');
    } finally {
      if (!quiet) {
        setRunnersLoading(false);
      }
    }
  }, []);

  const loadSessions = useCallback(async ({ keepSelection = false, quiet = false } = {}) => {
    if (!sliceId) {
      setSessions([]);
      setSelectedSessionId('');
      return;
    }
    if (!quiet) {
      setSessionsLoading(true);
      setSessionsError('');
    }
    try {
      const nextSessions = (await listAgentSessions(sliceId, { limit: 50 })).map(normalizeSession);
      setSessionsError('');
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
      if (quiet) {
        return;
      }
      setSessions([]);
      setSelectedSessionId('');
      setSessionsError(err?.message || 'Unable to load agent sessions.');
    } finally {
      if (!quiet) {
        setSessionsLoading(false);
      }
    }
  }, [normalizedRouteSessionId, sliceId]);

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
    if (!routeSession) {
      return;
    }
    if (routeSession.runnerId && onlineRunnerIds.has(routeSession.runnerId)) {
      setSelectedRunnerId(routeSession.runnerId);
    }
    setSelectedSessionId(routeSession.sessionId);
  }, [onlineRunnerIds, routeSession]);

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
    let retryTimer = 0;
    let controller = null;

    const refreshAgents = async ({ quiet = false } = {}) => {
      if (!active) return;
      await Promise.all([
        loadRunners({ keepSelection: true, quiet }),
        loadSessions({ keepSelection: true, quiet }),
      ]);
    };

    const watchRunnerUpdates = async () => {
      if (!active || typeof window === 'undefined') return;
      controller = new AbortController();
      try {
        const changed = await waitForAgentRunnerUpdates({
          timeoutMs: RUNNER_UPDATE_WAIT_MS,
          signal: controller.signal,
        });
        controller = null;
        if (!active) return;
        if (changed) {
          await refreshAgents({ quiet: true });
        }
        watchRunnerUpdates();
      } catch (err) {
        controller = null;
        if (!active || err?.name === 'AbortError' || typeof window === 'undefined') {
          return;
        }
        retryTimer = window.setTimeout(watchRunnerUpdates, RUNNER_WATCH_RETRY_MS);
      }
    };

    refreshAgents();
    watchRunnerUpdates();
    return () => {
      active = false;
      if (controller) {
        controller.abort();
      }
      if (typeof window !== 'undefined') {
        window.clearTimeout(retryTimer);
      }
    };
  }, [loadRunners, loadSessions]);

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
    capabilities,
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
    sessions,
    sessionsError,
    sessionsLoading,
    setEventPollingBusy,
  };
}
