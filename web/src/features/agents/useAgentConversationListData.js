import { useCallback, useState } from 'react';

import { listAgentSessions } from '../../api/agents.js';
import { normalizeSession } from './agentModels.js';

export function useAgentConversationListData({
  normalizedRouteSessionId,
  sliceId,
}) {
  const [sessions, setSessions] = useState([]);
  const [selectedSessionId, setSelectedSessionId] = useState('');
  const [sessionsLoading, setSessionsLoading] = useState(false);
  const [sessionsError, setSessionsError] = useState('');

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

  return {
    loadSessions,
    selectedSessionId,
    sessions,
    sessionsError,
    sessionsLoading,
    setSelectedSessionId,
  };
}
