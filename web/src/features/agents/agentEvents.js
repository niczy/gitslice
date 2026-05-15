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
const CHECKOUT_FAILURE_CONTROL_ERROR_CODES = new Set(['LOCAL_AGENT_CHECKOUT_FAILED']);

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

function textValue(value) {
  if (value == null) {
    return '';
  }
  if (Array.isArray(value)) {
    return value.map(textValue).filter(Boolean).join(' ');
  }
  if (typeof value === 'number' || typeof value === 'boolean') {
    return String(value);
  }
  if (typeof value !== 'string') {
    return '';
  }
  return value.trim();
}

function firstTextValue(...values) {
  for (const value of values) {
    const text = textValue(value);
    if (text) {
      return text;
    }
  }
  return '';
}

function plainObject(value) {
  if (!value) {
    return {};
  }
  if (typeof value === 'object' && !Array.isArray(value)) {
    return value;
  }
  if (typeof value === 'string') {
    try {
      const parsed = JSON.parse(value);
      return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : {};
    } catch {
      return {};
    }
  }
  return {};
}

function tokenName(value) {
  return textValue(value)
    .replace(/([a-z0-9])([A-Z])/g, '$1_$2')
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '');
}

function humanizeName(value) {
  const text = textValue(value)
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/[_-]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
  if (!text) {
    return '';
  }
  return text.split(' ').map((word) => {
    const lower = word.toLowerCase();
    if (['api', 'cli', 'http', 'https', 'mcp', 'url'].includes(lower)) {
      return lower.toUpperCase();
    }
    return `${lower.slice(0, 1).toUpperCase()}${lower.slice(1)}`;
  }).join(' ');
}

function commandValue(...values) {
  for (const value of values) {
    if (Array.isArray(value)) {
      const command = value.map((part) => String(part)).join(' ').trim();
      if (command) {
        return command;
      }
    }
    if (value && typeof value === 'object') {
      const nested = commandValue(value.command, value.cmd, value.shellCommand, value.shell_command);
      if (nested) {
        return nested;
      }
    }
    const text = textValue(value);
    if (text) {
      return text;
    }
  }
  return '';
}

const TOOL_ACTION_TITLES = {
  apply_patch: 'Apply patch',
  bash: 'Run command',
  command: 'Run command',
  command_exec_output: 'Command output',
  command_execution: 'Run command',
  dynamic_tool_call: 'Use tool',
  edit: 'Edit file',
  edit_file: 'Edit file',
  exec: 'Run command',
  file_change: 'Edit file',
  glob: 'Find files',
  grep: 'Search files',
  list: 'List files',
  ls: 'List files',
  mcp_tool_call: 'Use MCP tool',
  multi_edit: 'Edit files',
  multiedit: 'Edit files',
  process_output: 'Process output',
  read: 'Read file',
  read_file: 'Read file',
  readfile: 'Read file',
  shell: 'Run command',
  terminal_interaction: 'Terminal interaction',
  todo_write: 'Update plan',
  web_fetch: 'Fetch URL',
  web_search: 'Search web',
  webfetch: 'Fetch URL',
  websearch: 'Search web',
  write: 'Write file',
  write_file: 'Write file',
};

function toolEventParts(event) {
  const payload = plainObject(event?.payload);
  const item = plainObject(payload.item);
  const input = plainObject(payload.input || item.input);
  const invocation = plainObject(payload.invocation || item.invocation);
  const args = plainObject(
    payload.arguments
    || payload.args
    || item.arguments
    || item.args
    || input.arguments
    || input.args
    || invocation.arguments
    || invocation.args,
  );
  const nestedPayload = plainObject(payload.payload);
  const eventType = firstTextValue(payload.eventType, payload.event_type);
  const itemType = firstTextValue(payload.itemType, payload.item_type, payload.type, item.type);
  const toolName = firstTextValue(
    payload.tool,
    payload.name,
    item.tool,
    item.name,
    invocation.tool,
    invocation.name,
    input.tool,
    input.name,
  );
  return {
    payload,
    item,
    input,
    invocation,
    args,
    nestedPayload,
    eventType,
    itemType,
    toolName,
    command: commandValue(
      payload.command,
      payload.cmd,
      payload.shellCommand,
      payload.shell_command,
      item.command,
      item.cmd,
      item.shellCommand,
      item.shell_command,
      input.command,
      input.cmd,
      input.shellCommand,
      input.shell_command,
      args.command,
      args.cmd,
      nestedPayload.command,
      nestedPayload.cmd,
    ),
    cwd: firstTextValue(payload.cwd, item.cwd, input.cwd, args.cwd),
    path: firstTextValue(
      payload.path,
      payload.filePath,
      payload.file_path,
      item.path,
      item.filePath,
      item.file_path,
      input.path,
      input.filePath,
      input.file_path,
      args.path,
      args.filePath,
      args.file_path,
    ),
    status: firstTextValue(payload.status, payload.state, item.status, item.state),
    text: firstTextValue(payload.text, payload.delta, payload.output, payload.message, nestedPayload.text, nestedPayload.message),
    id: firstTextValue(payload.id, payload.itemId, payload.item_id, item.id),
  };
}

function toolBaseTitle(parts) {
  if (parts.command) {
    return 'Run command';
  }
  if (parts.path && tokenName(parts.itemType) === 'file_change') {
    return 'Edit file';
  }
  return TOOL_ACTION_TITLES[tokenName(parts.eventType)]
    || TOOL_ACTION_TITLES[tokenName(parts.toolName)]
    || (parts.toolName ? `Use ${humanizeName(parts.toolName)}` : '')
    || TOOL_ACTION_TITLES[tokenName(parts.itemType)]
    || 'Tool';
}

function toolTitle(event) {
  const parts = toolEventParts(event);
  const type = tokenName(event?.type);
  const eventType = tokenName(parts.eventType);
  const base = toolBaseTitle(parts);

  if (type === 'output' || type === 'result') {
    if (eventType === 'terminal_interaction') {
      return 'Terminal interaction';
    }
    if (base === 'Run command' || eventType === 'command_exec_output') {
      return 'Command output';
    }
    if (eventType === 'process_output') {
      return 'Process output';
    }
    return base === 'Tool' ? 'Tool output' : `${base} output`;
  }
  if (type === 'end') {
    if (base === 'Run command' || eventType === 'command_exec_output') {
      return 'Command finished';
    }
    return base === 'Tool' ? 'Tool finished' : `${base} finished`;
  }
  if (type === 'request') {
    return base === 'Tool' ? 'Tool requested' : `${base} requested`;
  }
  return base;
}

const TOOL_SUMMARY_SKIP_KEYS = new Set([
  'args',
  'arguments',
  'cmd',
  'command',
  'cwd',
  'description',
  'file_path',
  'filePath',
  'input',
  'message',
  'output',
  'path',
  'shell_command',
  'shellCommand',
  'status',
  'text',
  'tool',
]);

function summaryValue(value) {
  if (value == null) {
    return '';
  }
  if (Array.isArray(value)) {
    const values = value.map(summaryValue).filter(Boolean);
    return values.length > 0 ? values.join(', ') : '';
  }
  if (typeof value === 'number' || typeof value === 'boolean') {
    return String(value);
  }
  if (typeof value === 'string') {
    return value.trim();
  }
  return '';
}

function summarizeObject(value, skipKeys = TOOL_SUMMARY_SKIP_KEYS) {
  const object = plainObject(value);
  const lines = [];
  for (const [key, rawValue] of Object.entries(object)) {
    if (skipKeys.has(key)) {
      continue;
    }
    const valueText = summaryValue(rawValue);
    if (!valueText) {
      continue;
    }
    lines.push(`${humanizeName(key)}: ${valueText}`);
    if (lines.length >= 5) {
      break;
    }
  }
  return lines;
}

function uniqueLines(lines) {
  const seen = new Set();
  const output = [];
  for (const line of lines) {
    const text = textValue(line);
    if (!text || seen.has(text)) {
      continue;
    }
    seen.add(text);
    output.push(text);
  }
  return output;
}

function toolBody(event) {
  const parts = toolEventParts(event);
  const type = tokenName(event?.type);
  if ((type === 'output' || type === 'result') && parts.text) {
    return parts.text;
  }

  if (tokenName(parts.eventType) === 'terminal_interaction') {
    return parts.text || 'Terminal interaction';
  }

  const rawExitCode = parts.payload.exitCode
    ?? parts.payload.exit_code
    ?? parts.item.exitCode
    ?? parts.item.exit_code;
  if (type === 'end') {
    const exitText = rawExitCode == null ? '' : `Exit code: ${rawExitCode}`;
    return uniqueLines([
      parts.status ? `Status: ${parts.status}` : '',
      exitText,
      parts.path ? `Path: ${parts.path}` : '',
      parts.id ? `ID: ${parts.id}` : '',
      !parts.status && !exitText && !parts.path && !parts.id ? 'Completed' : '',
    ]).join('\n');
  }

  const description = firstTextValue(
    parts.payload.description,
    parts.item.description,
    parts.input.description,
    parts.args.description,
  );
  const lines = uniqueLines([
    parts.command,
    parts.path ? `Path: ${parts.path}` : '',
    parts.cwd ? `cwd: ${parts.cwd}` : '',
    description ? `Description: ${description}` : '',
    parts.status ? `Status: ${parts.status}` : '',
    ...summarizeObject(parts.args),
    ...summarizeObject(parts.input),
    ...summarizeObject(parts.invocation),
  ]);
  return lines.join('\n') || 'No additional details';
}

function orderedConversationEvents(events) {
  return (events || [])
    .map((event, index) => ({ event, index }))
    .sort((a, b) => (Number(a.event.seq || 0) - Number(b.event.seq || 0)) || (a.index - b.index))
    .map(({ event }) => event);
}

function shouldRenderConversationEvent(event) {
  if (event?.stream === 'status') {
    return false;
  }
  if (event?.stream !== 'control') {
    return true;
  }
  if (event?.type !== 'error') {
    return false;
  }
  if (CHECKOUT_FAILURE_CONTROL_ERROR_CODES.has(controlErrorCode(event))) {
    return false;
  }
  return isTerminalControlError(event);
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
    return toolTitle(event);
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
  if (event.stream === 'tool') {
    return toolBody(event);
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

export function latestCheckoutFailureEvent(events) {
  const failure = latestEvent(events, (event) => (
    event.stream === 'control'
    && event.type === 'error'
    && CHECKOUT_FAILURE_CONTROL_ERROR_CODES.has(controlErrorCode(event))
  ));
  if (!failure) {
    return null;
  }
  const attached = latestEvent(events, (event) => event.stream === 'status' && event.type === 'local_runner_attached');
  if (attached?.seq > failure.seq) {
    return null;
  }
  return failure;
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
  for (const event of orderedConversationEvents(events)) {
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
  const orderedEvents = orderedConversationEvents(events);
  let folded = [];
  let thinkingItem = null;
  let responseDraftItem = null;
  let responseDraftItems = [];
  let renderedLiveActivity = false;
  const pendingInputSeq = liveStreamState?.pendingInputSeq || 0;
  const agentLabel = agentDisplayName(session?.agentType);
  const isLiveEvent = (event) => Boolean(liveStreamState?.active && pendingInputSeq > 0 && event.seq > pendingInputSeq);

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
    thinkingItem = null;
    responseDraftItem = null;
  };

  const appendThinking = (event) => {
    const text = payloadText(event.payload);
    if (!text) {
      return;
    }
    if (isLiveEvent(event)) {
      renderedLiveActivity = true;
    }
    flushFolded();
    if (!thinkingItem) {
      thinkingItem = {
        kind: 'thinking',
        key: `thinking-${event.seq}`,
        text: '',
        ts: event.ts,
        live: isLiveEvent(event),
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
    if (isLiveEvent(event)) {
      renderedLiveActivity = true;
    }
    flushFolded();
    if (!responseDraftItem) {
      responseDraftItem = {
        kind: 'response-draft',
        key: `response-${event.seq}`,
        label: agentLabel,
        text: '',
        ts: event.ts,
        live: isLiveEvent(event),
      };
      items.push(responseDraftItem);
      responseDraftItems.push(responseDraftItem);
    }
    responseDraftItem.text += text;
  };

  const removeResponseDrafts = () => {
    for (const draft of responseDraftItems) {
      const index = items.indexOf(draft);
      if (index >= 0) {
        items.splice(index, 1);
      }
    }
    responseDraftItem = null;
    responseDraftItems = [];
  };

  for (const event of orderedEvents) {
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
        responseDraftItems = [];
      } else {
        removeResponseDrafts();
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
    } else if (event.stream === 'agent' && event.type === 'output_final' && responseDraftItems.length > 0) {
      thinkingItem = null;
      responseDraftItem = null;
      responseDraftItems = [];
    } else {
      if (!shouldRenderConversationEvent(event)) {
        continue;
      }
      if (isLiveEvent(event) && event.stream === 'tool') {
        renderedLiveActivity = true;
      }
      folded.push(event);
    }
  }
  flushFolded();

  if (liveStreamState?.active && !renderedLiveActivity) {
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
