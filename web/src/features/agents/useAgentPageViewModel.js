import { useMemo } from 'react';

import {
  buildConversationItems,
  latestCheckoutFailureEvent,
  latestRunnerState,
} from './agentEvents.js';
import {
  buildRunningAgentInfoRows,
  conversationAvailabilityLabel,
  infoValue,
  isConversationCloudOnly,
  isConversationLocal,
  runnerStateValue,
} from './agentModels.js';
import { getSliceDisplayName } from '../../utils/slices.js';

export function useAgentPageViewModel({
  events,
  liveStreamState,
  localChangesPanelOpen,
  runnerSessions,
  selectedRunner,
  selectedRunnerSessions,
  selectedSession,
  selectedSessionId,
  sessionsError,
  sliceId,
  slices,
}) {
  const currentSlice = useMemo(() => (
    (slices || []).find((slice) => slice.slice_id === sliceId) || null
  ), [sliceId, slices]);
  const sliceLabel = getSliceDisplayName(currentSlice?.name || sliceId || 'Slice');
  const canCreateSession = Boolean(sliceId && selectedRunner?.runnerId);
  const runnerState = useMemo(() => latestRunnerState(events), [events]);
  const runningAgentInfoRows = useMemo(
    () => buildRunningAgentInfoRows(selectedRunner, selectedSession, runnerState),
    [runnerState, selectedRunner, selectedSession],
  );
  const runnerHost = runnerStateValue(runnerState, 'host_name', 'hostName') || infoValue(selectedRunner?.hostName);
  const runnerPID = infoValue(runnerState?.pid || selectedRunner?.pid);
  const runnerRunningDir = runnerStateValue(runnerState, 'running_dir', 'runningDir')
    || runnerStateValue(runnerState, 'checkout_dir', 'checkoutDir')
    || infoValue(selectedRunner?.workspaceRoot);
  const runningAgentSummary = selectedRunner
    ? [
      selectedRunner.agentType || 'agent',
      runnerHost || 'local',
      runnerPID ? `pid ${runnerPID}` : '',
    ].filter(Boolean).join(' · ')
    : 'No running agent online';
  const selectedRunnerLocalCount = selectedRunnerSessions.filter(isConversationLocal).length;
  const selectedRunnerCloudOnlyCount = selectedRunnerSessions.filter(isConversationCloudOnly).length;
  const selectedRunnerConversationCountLabel = [
    selectedRunnerLocalCount === 1 ? '1 local conversation' : `${selectedRunnerLocalCount} local conversations`,
    selectedRunnerCloudOnlyCount > 0 ? `${selectedRunnerCloudOnlyCount} cloud-only` : '',
  ].filter(Boolean).join(' · ');
  const selectedSessionIsLocal = Boolean(selectedSession && isConversationLocal(selectedSession));
  const canRestartRunner = Boolean(
    selectedSession
    && selectedSession.provider === 'local'
    && selectedRunner?.runnerId
    && selectedSessionIsLocal,
  );
  const checkoutFailureEvent = useMemo(() => latestCheckoutFailureEvent(events), [events]);
  const checkoutFailure = checkoutFailureEvent
    ? {
      message: checkoutFailureEvent.payload?.message || 'The local runner could not create the checkout for this conversation.',
      ts: checkoutFailureEvent.ts,
    }
    : null;
  const canSendInput = Boolean(selectedSessionId && selectedSession && selectedSessionIsLocal && !checkoutFailure);
  const conversationItems = useMemo(
    () => buildConversationItems(events, liveStreamState, selectedSession),
    [events, liveStreamState, selectedSession],
  );
  const hasRunnerConversation = runnerSessions.length > 0;
  const showAgentSessionDocsLink = !sessionsError && !hasRunnerConversation;
  const localChangesPanelAvailable = selectedSessionIsLocal;
  const localChangesPanelVisible = Boolean(localChangesPanelAvailable && localChangesPanelOpen);
  const selectedSessionSubtitle = selectedSession
    ? `${conversationAvailabilityLabel(selectedSession)} · ${selectedSession.agentType || 'agent'} · ${selectedSession.sessionId}`
    : '';

  return {
    canCreateSession,
    canRestartRunner,
    canSendInput,
    checkoutFailure,
    conversationItems,
    currentSlice,
    localChangesPanelAvailable,
    localChangesPanelVisible,
    runningAgentInfoRows,
    runningAgentSummary,
    runnerRunningDir,
    selectedRunnerConversationCountLabel,
    selectedSessionIsLocal,
    selectedSessionSubtitle,
    showAgentSessionDocsLink,
    sliceLabel,
  };
}
