import { useCallback, useEffect, useState } from 'react';

import {
  getAgentCapabilities,
  listAgentRunners,
  waitForAgentRunnerUpdates,
} from '../../api/agents.js';
import { normalizeRunner } from './agentModels.js';

const RUNNER_UPDATE_WAIT_MS = 25000;
const RUNNER_WATCH_RETRY_MS = 5000;

export function useAgentRunnersData({
  loadSessions,
  setSelectedRunnerId,
}) {
  const [runners, setRunners] = useState([]);
  const [runnersLoading, setRunnersLoading] = useState(false);
  const [runnersError, setRunnersError] = useState('');
  const [capabilities, setCapabilities] = useState(null);

  const defaultAgentType = capabilities?.defaultAgentType || capabilities?.default_agent_type || '';

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
  }, [setSelectedRunnerId]);

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

  return {
    capabilities,
    defaultAgentType,
    loadRunners,
    runners,
    runnersError,
    runnersLoading,
  };
}
