import { useCallback, useEffect, useMemo, useState } from 'react';

import {
  getAgentCapabilities,
  listAgentRunners,
  listAgentSessionEvents,
  listAgentSessions,
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
  normalizeRunner,
  normalizeSession,
} from './agentModels.js';

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
    if (typeof window === 'undefined') {
      return undefined;
    }
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

  useEffect(() => {
    setEvents([]);
    setEventsError('');
  }, [selectedSessionId]);

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
      if (!active) return;
      await loadSelectedEvents();
    };

    loadEvents();
    const intervalId = window.setInterval(loadEvents, pollIntervalMs);
    return () => {
      active = false;
      window.clearInterval(intervalId);
    };
  }, [assistantStreaming, eventPollingBusy, loadSelectedEvents, selectedSessionId]);

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
