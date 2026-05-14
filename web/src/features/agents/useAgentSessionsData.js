import { useCallback, useEffect, useMemo, useState } from 'react';

import { writeAgentSessionURL } from './agentLayout.js';
import {
  conversationAvailabilityRank,
} from './agentModels.js';
import { useAgentConversationListData } from './useAgentConversationListData.js';
import { useAgentRunnersData } from './useAgentRunnersData.js';
import { useAgentSessionEventsData } from './useAgentSessionEventsData.js';

export function useAgentSessionsData({
  onSelectSession,
  routeSessionId = '',
  sliceId,
}) {
  const [selectedRunnerId, setSelectedRunnerId] = useState('');

  const normalizedRouteSessionId = String(routeSessionId || '').trim();
  const {
    loadSessions,
    selectedSessionId,
    sessions,
    sessionsError,
    sessionsLoading,
    setSelectedSessionId,
  } = useAgentConversationListData({
    normalizedRouteSessionId,
    sliceId,
  });
  const {
    capabilities,
    defaultAgentType,
    loadRunners,
    runners,
    runnersError,
    runnersLoading,
  } = useAgentRunnersData({
    loadSessions,
    setSelectedRunnerId,
  });

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
  const {
    assistantStreaming,
    events,
    eventsError,
    eventsLoading,
    liveStreamState,
    loadSelectedEvents,
    setEventPollingBusy,
  } = useAgentSessionEventsData({
    selectedSession,
    selectedSessionId,
  });

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
