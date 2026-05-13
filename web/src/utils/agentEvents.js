import { decodeBase64UTF8 } from '../../shared/runtime.js';

export function parseAgentEventPayload(value) {
  if (!value) {
    return {};
  }
  if (typeof value === 'object') {
    return value;
  }
  if (typeof value !== 'string') {
    return {};
  }

  const candidates = [value];
  try {
    candidates.push(decodeBase64UTF8(value));
  } catch {
    // Some local adapters already return JSON strings.
  }

  for (const candidate of candidates) {
    try {
      const parsed = JSON.parse(candidate);
      return parsed && typeof parsed === 'object' ? parsed : { text: String(parsed) };
    } catch {
      // Try the next representation.
    }
  }

  return { text: value };
}
