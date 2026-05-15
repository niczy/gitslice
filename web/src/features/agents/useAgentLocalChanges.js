import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

import {
  requestAgentSessionChangesetExport,
  requestAgentSessionLocalChanges,
} from '../../api/agents.js';
import { LOCAL_CHANGES_REQUEST_TIMEOUT_MS } from './agentConstants.js';
import {
  latestChangesetExportEvent,
  latestChangesetExportFailureEvent,
  latestEventSeq,
  latestLocalChangesEvent,
  latestLocalChangesFailureEvent,
  payloadRequestId,
} from './agentEvents.js';
import { normalizeLocalChangesPayload } from './agentLocalChanges.js';
import { isConversationLocal } from './agentModels.js';

export function useAgentLocalChanges({
  assistantStreaming,
  canSendInput,
  events,
  loadSelectedEvents,
  localChangesPanelOpen,
  selectedSession,
  selectedSessionId,
  setEventPollingBusy,
}) {
  const [localChangesRequesting, setLocalChangesRequesting] = useState(false);
  const [exportingChangeset, setExportingChangeset] = useState(false);
  const [pendingLocalChangesRequestId, setPendingLocalChangesRequestId] = useState('');
  const [pendingLocalChangesRequestedAt, setPendingLocalChangesRequestedAt] = useState(0);
  const [pendingChangesetExportRequestId, setPendingChangesetExportRequestId] = useState('');
  const [changesetMessage, setChangesetMessage] = useState('');
  const [localChangesError, setLocalChangesError] = useState('');
  const autoLocalChangesSessionRef = useRef('');
  const autoLocalChangesOutputSeqRef = useRef(0);

  const localChangesEvent = useMemo(() => latestLocalChangesEvent(events), [events]);
  const localChangesFailureEvent = useMemo(() => latestLocalChangesFailureEvent(events), [events]);
  const localChanges = useMemo(() => (
    localChangesEvent ? normalizeLocalChangesPayload(localChangesEvent.payload) : null
  ), [localChangesEvent]);
  const latestExportEvent = useMemo(() => latestChangesetExportEvent(events), [events]);
  const latestExportFailureEvent = useMemo(() => latestChangesetExportFailureEvent(events), [events]);
  const latestAgentOutputFinalSeq = useMemo(
    () => latestEventSeq(events, (event) => event.stream === 'agent' && event.type === 'output_final'),
    [events],
  );
  const localChangesBusy = localChangesRequesting || Boolean(pendingLocalChangesRequestId);
  const changesetExportBusy = exportingChangeset || Boolean(pendingChangesetExportRequestId);
  const hasDirtyFiles = Boolean(localChanges && localChanges.pathCount > 0);
  const canExportChangeset = Boolean(
    canSendInput
    && hasDirtyFiles
    && !assistantStreaming
    && !changesetExportBusy
  );
  const latestChangesetExportPayload = latestExportEvent?.payload || {};
  const latestExportedChangesetId = latestChangesetExportPayload.changeset_id || latestChangesetExportPayload.changesetId || '';
  const latestLocalChangesFailureMessage = localChangesFailureEvent && localChangesFailureEvent.seq > (localChangesEvent?.seq || 0)
    ? localChangesFailureEvent.payload?.message || ''
    : '';
  const latestExportFailureMessage = latestExportFailureEvent && latestExportFailureEvent.seq > (latestExportEvent?.seq || 0)
    ? latestExportFailureEvent.payload?.message || ''
    : '';
  const localChangesDisplayError = localChangesError
    || latestLocalChangesFailureMessage
    || latestExportFailureMessage
    || '';

  const requestLocalChanges = useCallback(async ({ silent = false, includeDiffs = localChangesPanelOpen } = {}) => {
    if (!selectedSessionId || !selectedSession || !isConversationLocal(selectedSession)) {
      return;
    }
    setLocalChangesRequesting(true);
    if (!silent) {
      setLocalChangesError('');
    }
    try {
      const result = await requestAgentSessionLocalChanges(selectedSessionId, { limit: 100, includeDiffs });
      setPendingLocalChangesRequestId(result.requestId || '');
      setPendingLocalChangesRequestedAt(Date.now());
      await loadSelectedEvents({ incremental: true });
    } catch (err) {
      setLocalChangesError(err?.message || 'Unable to refresh local changes.');
      setPendingLocalChangesRequestId('');
      setPendingLocalChangesRequestedAt(0);
    } finally {
      setLocalChangesRequesting(false);
    }
  }, [loadSelectedEvents, localChangesPanelOpen, selectedSession, selectedSessionId]);

  const handleExportChangeset = useCallback(async () => {
    if (!canExportChangeset || !selectedSessionId) {
      return;
    }
    setExportingChangeset(true);
    setLocalChangesError('');
    try {
      const result = await requestAgentSessionChangesetExport(selectedSessionId, {
        message: changesetMessage.trim(),
      });
      setPendingChangesetExportRequestId(result.requestId || '');
      await loadSelectedEvents({ incremental: true });
    } catch (err) {
      setLocalChangesError(err?.message || 'Unable to export changeset.');
    } finally {
      setExportingChangeset(false);
    }
  }, [canExportChangeset, changesetMessage, loadSelectedEvents, selectedSessionId]);

  useEffect(() => {
    if (!setEventPollingBusy) {
      return undefined;
    }
    const busy = localChangesBusy || changesetExportBusy;
    setEventPollingBusy(busy);
    return () => {
      if (busy) {
        setEventPollingBusy(false);
      }
    };
  }, [changesetExportBusy, localChangesBusy, setEventPollingBusy]);

  useEffect(() => {
    setLocalChangesError('');
    setPendingLocalChangesRequestId('');
    setPendingLocalChangesRequestedAt(0);
    setPendingChangesetExportRequestId('');
    setChangesetMessage('');
    autoLocalChangesOutputSeqRef.current = 0;
  }, [selectedSessionId]);

  useEffect(() => {
    if (!pendingLocalChangesRequestId) {
      return;
    }
    const matched = [localChangesEvent, localChangesFailureEvent]
      .filter(Boolean)
      .some((event) => payloadRequestId(event.payload) === pendingLocalChangesRequestId);
    if (matched) {
      setPendingLocalChangesRequestId('');
      setPendingLocalChangesRequestedAt(0);
      if (localChangesEvent && payloadRequestId(localChangesEvent.payload) === pendingLocalChangesRequestId) {
        setLocalChangesError('');
      }
    }
  }, [localChangesEvent, localChangesFailureEvent, pendingLocalChangesRequestId]);

  useEffect(() => {
    if (!pendingLocalChangesRequestId || !pendingLocalChangesRequestedAt || typeof window === 'undefined') {
      return undefined;
    }
    const elapsed = Date.now() - pendingLocalChangesRequestedAt;
    const timeoutDelay = Math.max(0, LOCAL_CHANGES_REQUEST_TIMEOUT_MS - elapsed);
    const timeoutId = window.setTimeout(() => {
      setPendingLocalChangesRequestId('');
      setPendingLocalChangesRequestedAt(0);
      setLocalChangesRequesting(false);
      setLocalChangesError('Runner did not return local changes.');
    }, timeoutDelay);
    return () => window.clearTimeout(timeoutId);
  }, [pendingLocalChangesRequestId, pendingLocalChangesRequestedAt]);

  useEffect(() => {
    if (!pendingChangesetExportRequestId) {
      return;
    }
    const matched = [latestExportEvent, latestExportFailureEvent]
      .filter(Boolean)
      .some((event) => payloadRequestId(event.payload) === pendingChangesetExportRequestId);
    if (matched) {
      setPendingChangesetExportRequestId('');
      setExportingChangeset(false);
      if (latestExportEvent && payloadRequestId(latestExportEvent.payload) === pendingChangesetExportRequestId) {
        setChangesetMessage('');
      }
    }
  }, [latestExportEvent, latestExportFailureEvent, pendingChangesetExportRequestId]);

  useEffect(() => {
    if (!selectedSessionId || !selectedSession || !isConversationLocal(selectedSession)) {
      return;
    }
    if (autoLocalChangesSessionRef.current === selectedSessionId) {
      return;
    }
    autoLocalChangesSessionRef.current = selectedSessionId;
    requestLocalChanges({ silent: true, includeDiffs: localChangesPanelOpen });
  }, [localChangesPanelOpen, requestLocalChanges, selectedSession, selectedSessionId]);

  useEffect(() => {
    if (!localChangesPanelOpen || !selectedSessionId || !selectedSession || !isConversationLocal(selectedSession)) {
      return;
    }
    if (localChangesBusy || localChanges?.diffsIncluded) {
      return;
    }
    requestLocalChanges({ silent: true, includeDiffs: true });
  }, [localChanges, localChangesBusy, localChangesPanelOpen, requestLocalChanges, selectedSession, selectedSessionId]);

  useEffect(() => {
    if (!selectedSessionId || !selectedSession || !isConversationLocal(selectedSession) || assistantStreaming) {
      return;
    }
    if (latestAgentOutputFinalSeq === 0 || autoLocalChangesOutputSeqRef.current >= latestAgentOutputFinalSeq) {
      return;
    }
    autoLocalChangesOutputSeqRef.current = latestAgentOutputFinalSeq;
    requestLocalChanges({ silent: true, includeDiffs: localChangesPanelOpen });
  }, [assistantStreaming, latestAgentOutputFinalSeq, localChangesPanelOpen, requestLocalChanges, selectedSession, selectedSessionId]);

  return {
    canExportChangeset,
    changesetExportLoading: changesetExportBusy,
    changesetMessage,
    hasDirtyFiles,
    latestExportedChangesetId,
    localChanges,
    localChangesDisplayError,
    localChangesLoading: localChangesBusy,
    requestLocalChanges,
    setChangesetMessage,
    handleExportChangeset,
  };
}
