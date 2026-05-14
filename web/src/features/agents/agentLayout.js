import {
  AGENTS_SIDEBAR_DEFAULT_WIDTH,
  AGENTS_SIDEBAR_MAX_WIDTH,
  AGENTS_SIDEBAR_MIN_WIDTH,
  AGENTS_SIDEBAR_WIDTH_STORAGE_KEY,
} from './agentConstants.js';

export function clampAgentsSidebarWidth(value, maxWidth = AGENTS_SIDEBAR_MAX_WIDTH) {
  const numeric = Number(value);
  if (!Number.isFinite(numeric)) {
    return AGENTS_SIDEBAR_DEFAULT_WIDTH;
  }
  return Math.min(Math.max(maxWidth, AGENTS_SIDEBAR_MIN_WIDTH), Math.max(AGENTS_SIDEBAR_MIN_WIDTH, numeric));
}

export function readAgentsSidebarWidth() {
  if (typeof window === 'undefined') {
    return AGENTS_SIDEBAR_DEFAULT_WIDTH;
  }
  try {
    return clampAgentsSidebarWidth(window.localStorage.getItem(AGENTS_SIDEBAR_WIDTH_STORAGE_KEY));
  } catch {
    return AGENTS_SIDEBAR_DEFAULT_WIDTH;
  }
}

export function writeAgentsSidebarWidth(value) {
  if (typeof window === 'undefined') {
    return;
  }
  try {
    window.localStorage.setItem(AGENTS_SIDEBAR_WIDTH_STORAGE_KEY, String(Math.round(value)));
  } catch {
    // Persisting panel width is a convenience only.
  }
}

export function writeAgentSessionURL(sessionId, { replace = false } = {}) {
  if (typeof window === 'undefined') {
    return;
  }
  const nextSessionId = String(sessionId || '').trim();
  try {
    const url = new URL(window.location.href);
    if (!url.pathname.endsWith('/agents')) {
      return;
    }
    if (nextSessionId) {
      url.searchParams.set('session', nextSessionId);
    } else {
      url.searchParams.delete('session');
    }
    const nextPath = `${url.pathname}${url.search}${url.hash}`;
    const currentPath = `${window.location.pathname}${window.location.search}${window.location.hash}`;
    if (nextPath === currentPath) {
      return;
    }
    const method = replace ? 'replaceState' : 'pushState';
    window.history[method](window.history.state, '', nextPath);
  } catch {
    // URL state is best-effort; selection still works without it.
  }
}
