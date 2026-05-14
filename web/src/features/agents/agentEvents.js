import { parseAgentEventPayload } from '../../utils/agentEvents.js';
import { renderMarkdownHtml } from '../../utils/markdown.js';
import { BLOCKED_CONVERSATION_STATES } from './agentConstants.js';
import {
  agentDisplayName,
  isConversationLocal,
  normalizeConversationState,
} from './agentModels.js';
import {
  localChangesSummaryText,
  normalizeLocalChangesPayload,
  payloadRequestId,
} from './agentLocalChanges.js';

const NON_TERMINAL_CONTROL_ERROR_CODES = new Set(['CODEX_CONFIG_WARNING']);

export function normalizeEvent(event) {
  return {
    seq: Number(event?.seq || 0),
    ts: event?.ts || '',
    stream: event?.stream || '',
    type: event?.type || '',
    kind: event?.kind || '',
    payload: parseAgentEventPayload(event?.payload),
  };
}

export function payloadText(payload) {
  return payload?.text
    || payload?.delta
    || payload?.message
    || payload?.status
    || payload?.state
    || payload?.tool
    || '';
}

export function payloadExitCode(payload) {
  const raw = payload?.exitCode ?? payload?.exit_code;
  const numeric = Number(raw);
  return Number.isFinite(numeric) ? numeric : 0;
}

export function latestEvent(events, predicate) {
  for (let i = events.length - 1; i >= 0; i -= 1) {
    if (predicate(events[i])) {
      return events[i];
    }
  }
  return null;
}

export function latestEventSeq(events, predicate) {
  return latestEvent(events, predicate)?.seq || 0;
}

export function controlErrorCode(event) {
  return String(event?.payload?.code || event?.payload?.errorCode || event?.payload?.error_code || '')
    .trim()
    .toUpperCase();
}

export function isTerminalControlError(event) {
  if (event?.stream !== 'control' || event?.type !== 'error') {
    return false;
  }
  return !NON_TERMINAL_CONTROL_ERROR_CODES.has(controlErrorCode(event));
}

export function renderConversationMarkdown(text) {
  return renderMarkdownHtml(text) || '<p></p>';
}

export function conversationMessageFromEvent(event, agentLabel = 'Agent') {
  if (event.stream === 'agent' && event.type === 'input') {
    return {
      key: `${event.seq}-user`,
      role: 'user',
      label: 'You',
      ts: event.ts,
      text: payloadText(event.payload),
      failed: false,
    };
  }
  if (event.stream === 'agent' && event.type === 'output_final') {
    const exitCode = payloadExitCode(event.payload);
    return {
      key: `${event.seq}-assistant`,
      role: exitCode === 0 ? 'assistant' : 'error',
      label: exitCode === 0 ? agentLabel : `${agentLabel} error`,
      ts: event.ts,
      text: payloadText(event.payload),
      failed: exitCode !== 0,
    };
  }
  return null;
}

export function eventKind(event) {
  if (event?.stream === 'control' && event?.type === 'error' && !isTerminalControlError(event)) {
    return 'control';
  }
  const explicit = String(event?.kind || '').trim().toLowerCase();
  if (explicit) {
    return explicit;
  }
  const stream = String(event?.stream || '').trim().toLowerCase();
  const type = String(event?.type || '').trim().toLowerCase();
  if (stream === 'agent' && type === 'input') return 'user_input';
  if (stream === 'agent' && ['thinking_delta', 'reasoning_delta', 'reasoning_summary_delta'].includes(type)) return 'thinking';
  if (stream === 'agent' && ['output_delta', 'output_final'].includes(type)) return 'model_response';
  if (stream === 'tool' && ['start', 'call', 'request'].includes(type)) return 'tool_call';
  if (stream === 'tool' && ['output', 'result', 'end'].includes(type)) return 'tool_result';
  if (stream === 'status') return 'status';
  if (stream === 'control' && type === 'error') return isTerminalControlError(event) ? 'error' : 'control';
  if (stream === 'control') return 'control';
  return 'event';
}

export function isThinkingEvent(event) {
  return eventKind(event) === 'thinking';
}

export function isModelResponseDelta(event) {
  return eventKind(event) === 'model_response' && event.type === 'output_delta';
}

export function eventTitle(event) {
  if (event.stream === 'status' && event.type === 'state') {
    return `State ${normalizeConversationState(event.payload?.state) || 'changed'}`;
  }
  if (event.stream === 'status' && event.type === 'local_runner_attached') {
    return 'Runner attached';
  }
  if (event.stream === 'status' && event.type === 'local_changes') {
    return 'Local changes';
  }
  if (event.stream === 'tool') {
    return event.payload?.tool || event.payload?.id || 'Tool';
  }
  if (event.stream === 'control') {
    switch (event.type) {
      case 'local_changes_requested':
        return 'Local changes requested';
      case 'local_changes_failed':
        return 'Local changes failed';
      case 'changeset_export_requested':
        return 'Changeset export requested';
      case 'changeset_export_started':
        return 'Changeset export started';
      case 'changeset_export_completed':
        return 'Changeset exported';
      case 'changeset_export_failed':
        return 'Changeset export failed';
      case 'local_runner_restart_requested':
        return 'Runner restart requested';
      case 'local_runner_restart_started':
        return 'Runner restart started';
      case 'local_runner_upgrade_completed':
        return 'Runner upgrade completed';
      case 'local_runner_restart_spawned':
        return 'Runner replacement started';
      case 'local_runner_restart_failed':
        return 'Runner restart failed';
      default:
        return event.payload?.code || 'Control';
    }
  }
  return event.stream || 'Agent';
}

export function eventBody(event) {
  if (event.stream === 'status' && event.type === 'state') {
    return normalizeConversationState(event.payload?.state) || JSON.stringify(event.payload || {});
  }
  if (event.stream === 'status' && event.type === 'local_runner_attached') {
    const host = event.payload?.host_name || event.payload?.hostName || '';
    const dir = event.payload?.running_dir || event.payload?.runningDir || event.payload?.checkout_dir || event.payload?.checkoutDir || '';
    return [host, dir].filter(Boolean).join(' · ') || JSON.stringify(event.payload || {});
  }
  if (event.stream === 'status' && event.type === 'local_changes') {
    const localChanges = normalizeLocalChangesPayload(event.payload || {});
    return [
      localChangesSummaryText(localChanges),
      localChanges.trackedChangesetId ? `tracked ${localChanges.trackedChangesetId}` : '',
      localChanges.refreshedAt || '',
    ].filter(Boolean).join(' · ') || JSON.stringify(event.payload || {});
  }
  if (event.stream === 'control' && event.type?.startsWith('changeset_export_')) {
    return [
      event.payload?.status,
      event.payload?.changeset_id || event.payload?.changesetId,
      event.payload?.message,
    ].filter(Boolean).join(' · ') || JSON.stringify(event.payload || {});
  }
  if (event.stream === 'control' && event.type?.startsWith('local_changes_')) {
    return [
      event.payload?.status,
      event.payload?.message,
      event.payload?.request_id || event.payload?.requestId,
    ].filter(Boolean).join(' · ') || JSON.stringify(event.payload || {});
  }
  if (event.stream === 'control' && event.type?.startsWith('local_runner_')) {
    const replacementPID = event.payload?.replacement_pid || event.payload?.replacementPid || '';
    return [
      event.payload?.status,
      event.payload?.action,
      event.payload?.message,
      replacementPID ? `replacement pid ${replacementPID}` : '',
    ].filter(Boolean).join(' · ') || JSON.stringify(event.payload || {});
  }
  return event.payload?.text
    || event.payload?.message
    || event.payload?.status
    || event.payload?.state
    || event.payload?.tool
    || JSON.stringify(event.payload || {});
}

export function eventTone(event) {
  const kind = eventKind(event);
  if (kind === 'error' || isTerminalControlError(event)) {
    return 'error';
  }
  if (kind === 'tool_call' || kind === 'tool_result' || event.stream === 'tool') {
    return 'tool';
  }
  if (event.stream === 'status') {
    return 'status';
  }
  if (event.stream === 'control') {
    return 'status';
  }
  return 'agent';
}

export function latestLocalChangesEvent(events) {
  return latestEvent(events, (event) => event.stream === 'status' && event.type === 'local_changes');
}

export function latestLocalChangesFailureEvent(events) {
  return latestEvent(events, (event) => event.stream === 'control' && event.type === 'local_changes_failed');
}

export function latestChangesetExportEvent(events) {
  return latestEvent(events, (event) => event.stream === 'control' && event.type === 'changeset_export_completed');
}

export function latestChangesetExportFailureEvent(events) {
  return latestEvent(events, (event) => event.stream === 'control' && event.type === 'changeset_export_failed');
}

export function buildLiveStreamState(events, session) {
  if (!session || !isConversationLocal(session) || BLOCKED_CONVERSATION_STATES.has(session.state) || session.availability === 'failed') {
    return {
      active: false,
      pendingInputSeq: 0,
      thinkingText: '',
      responseText: '',
    };
  }

  let pendingInputSeq = 0;
  let thinkingText = '';
  let responseText = '';
  for (const event of events) {
    if (event.stream === 'agent' && event.type === 'input' && payloadText(event.payload).trim()) {
      pendingInputSeq = event.seq;
      thinkingText = '';
      responseText = '';
    } else if (event.stream === 'agent' && event.type === 'output_final') {
      pendingInputSeq = 0;
      thinkingText = '';
      responseText = '';
    } else if (isTerminalControlError(event)) {
      pendingInputSeq = 0;
      thinkingText = '';
      responseText = '';
    } else if (event.stream === 'status' && event.type === 'state' && BLOCKED_CONVERSATION_STATES.has(normalizeConversationState(event.payload?.state))) {
      pendingInputSeq = 0;
      thinkingText = '';
      responseText = '';
    } else if (pendingInputSeq > 0 && event.seq > pendingInputSeq && isThinkingEvent(event)) {
      thinkingText += payloadText(event.payload);
    } else if (pendingInputSeq > 0 && event.seq > pendingInputSeq && isModelResponseDelta(event)) {
      responseText += payloadText(event.payload);
    }
  }

  return {
    active: pendingInputSeq > 0,
    pendingInputSeq,
    thinkingText,
    responseText,
  };
}

export function buildConversationItems(events, liveStreamState = { active: false }, session = null) {
  const items = [];
  let folded = [];
  let thinkingItem = null;
  let responseDraftItem = null;
  const pendingInputSeq = liveStreamState?.pendingInputSeq || 0;
  const agentLabel = agentDisplayName(session?.agentType);

  const flushFolded = () => {
    if (folded.length === 0) {
      return;
    }
    items.push({
      kind: 'events',
      key: `events-${folded[0].seq}-${folded[folded.length - 1].seq}`,
      events: folded,
    });
    folded = [];
  };

  const appendThinking = (event) => {
    const text = payloadText(event.payload);
    if (!text) {
      return;
    }
    flushFolded();
    if (!thinkingItem) {
      thinkingItem = {
        kind: 'thinking',
        key: `thinking-${event.seq}`,
        text: '',
        ts: event.ts,
        live: false,
      };
      items.push(thinkingItem);
    }
    thinkingItem.text += text;
  };

  const appendResponseDelta = (event) => {
    const text = payloadText(event.payload);
    if (!text) {
      return;
    }
    flushFolded();
    if (!responseDraftItem) {
      responseDraftItem = {
        kind: 'response-draft',
        key: `response-${event.seq}`,
        label: agentLabel,
        text: '',
        ts: event.ts,
        live: false,
      };
      items.push(responseDraftItem);
    }
    responseDraftItem.text += text;
  };

  const removeResponseDraft = () => {
    if (!responseDraftItem) {
      return;
    }
    const index = items.indexOf(responseDraftItem);
    if (index >= 0) {
      items.splice(index, 1);
    }
    responseDraftItem = null;
  };

  for (const event of events) {
    if (pendingInputSeq > 0 && event.seq > pendingInputSeq && (isThinkingEvent(event) || isModelResponseDelta(event))) {
      continue;
    }
    if (isThinkingEvent(event)) {
      appendThinking(event);
      continue;
    }
    if (isModelResponseDelta(event)) {
      appendResponseDelta(event);
      continue;
    }
    const message = conversationMessageFromEvent(event, agentLabel);
    if (message?.text) {
      if (message.role === 'user') {
        thinkingItem = null;
        responseDraftItem = null;
      } else {
        removeResponseDraft();
      }
      flushFolded();
      items.push({
        kind: 'message',
        key: message.key,
        message,
      });
      if (message.role !== 'user') {
        thinkingItem = null;
        responseDraftItem = null;
      }
    } else if (event.stream === 'agent' && event.type === 'output_final' && responseDraftItem?.text) {
      thinkingItem = null;
      responseDraftItem = null;
    } else {
      folded.push(event);
    }
  }
  flushFolded();

  if (liveStreamState?.active && liveStreamState.thinkingText?.trim()) {
    items.push({
      kind: 'thinking',
      key: 'assistant-thinking',
      text: liveStreamState.thinkingText,
      live: true,
    });
  }

  if (liveStreamState?.active && liveStreamState.responseText?.trim()) {
    items.push({
      kind: 'response-draft',
      key: 'assistant-response-draft',
      label: agentLabel,
      text: liveStreamState.responseText,
      live: true,
    });
  }

  if (liveStreamState?.active && !liveStreamState.thinkingText?.trim() && !liveStreamState.responseText?.trim()) {
    items.push({
      kind: 'streaming',
      key: 'assistant-streaming',
      label: agentLabel,
    });
  }

  return items;
}

export function latestRunnerState(events) {
  for (let i = events.length - 1; i >= 0; i -= 1) {
    const event = events[i];
    if (event.stream === 'status' && event.type === 'local_runner_attached') {
      return event.payload || {};
    }
  }
  return {};
}

export { payloadRequestId };
