import { useCallback, useEffect, useState } from 'react';

import {
  createAgentSession,
  requestAgentRunnerRestart,
  sendAgentSessionInput,
} from '../../api/agents.js';
import { normalizeSession } from './agentModels.js';

export function useAgentSessionActions({
  canCreateSession,
  canRestartRunner,
  canSendInput,
  defaultAgentType,
  loadSelectedEvents,
  loadSessions,
  selectSessionId,
  selectedRunner,
  selectedRunnerId,
  selectedSessionId,
  sliceId,
}) {
  const [creatingSession, setCreatingSession] = useState(false);
  const [inputText, setInputText] = useState('');
  const [sendingInput, setSendingInput] = useState(false);
  const [runnerActionLoading, setRunnerActionLoading] = useState(false);
  const [createError, setCreateError] = useState('');
  const [inputError, setInputError] = useState('');
  const [runnerActionError, setRunnerActionError] = useState('');

  const handleCreateSession = useCallback(async () => {
    if (!canCreateSession || creatingSession || !selectedRunner?.runnerId) {
      return;
    }
    setCreatingSession(true);
    setCreateError('');
    try {
      const created = normalizeSession(await createAgentSession(sliceId, {
        runnerId: selectedRunner.runnerId,
        agentType: selectedRunner.agentType || defaultAgentType,
      }));
      await loadSessions({ keepSelection: true });
      selectSessionId(created.sessionId, { runnerId: created.runnerId || selectedRunner.runnerId });
    } catch (err) {
      setCreateError(err?.message || 'Unable to create agent session.');
    } finally {
      setCreatingSession(false);
    }
  }, [
    canCreateSession,
    creatingSession,
    defaultAgentType,
    loadSessions,
    selectSessionId,
    selectedRunner,
    sliceId,
  ]);

  const handleSendInput = useCallback(async (event) => {
    event.preventDefault();
    const text = inputText.trim();
    if (!canSendInput || !text || sendingInput) {
      return;
    }
    setSendingInput(true);
    setInputError('');
    try {
      await sendAgentSessionInput(selectedSessionId, text);
      setInputText('');
      await loadSelectedEvents();
    } catch (err) {
      setInputError(err?.message || 'Unable to send agent input.');
    } finally {
      setSendingInput(false);
    }
  }, [canSendInput, inputText, loadSelectedEvents, selectedSessionId, sendingInput]);

  const handleUpgradeRestartRunner = useCallback(async () => {
    if (!selectedSessionId || runnerActionLoading || !canRestartRunner) {
      return;
    }
    setRunnerActionLoading(true);
    setRunnerActionError('');
    try {
      await requestAgentRunnerRestart(selectedSessionId, {
        upgrade: true,
        reason: 'web_ui',
      });
      await loadSelectedEvents();
    } catch (err) {
      setRunnerActionError(err?.message || 'Unable to request agent restart.');
    } finally {
      setRunnerActionLoading(false);
    }
  }, [canRestartRunner, loadSelectedEvents, runnerActionLoading, selectedSessionId]);

  useEffect(() => {
    setRunnerActionError('');
  }, [selectedRunnerId, selectedSessionId]);

  return {
    createError,
    creatingSession,
    handleCreateSession,
    handleSendInput,
    handleUpgradeRestartRunner,
    inputError,
    inputText,
    runnerActionError,
    runnerActionLoading,
    sendingInput,
    setInputText,
  };
}
