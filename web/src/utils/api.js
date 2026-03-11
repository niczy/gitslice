// ---------------------------------------------------------------------------
// API and authentication helpers
// ---------------------------------------------------------------------------

import { getSignedInUsername } from '../auth.js';

export const apiBaseUrl = import.meta.env.VITE_FILE_API_BASE_URL || '';

export function currentUsername() {
  return getSignedInUsername();
}

export function fetchWithAuth(url, options = {}) {
  const headers = new Headers(options.headers || {});
  const username = currentUsername();
  if (username) {
    headers.set('Authorization', `User ${username}`);
  }
  return fetch(url, { ...options, credentials: 'include', headers });
}

async function readErrorMessage(response, fallback) {
  let detail = '';
  try {
    const text = await response.text();
    if (text) {
      try {
        const payload = JSON.parse(text);
        detail = payload?.message || payload?.error || '';
      } catch {
        detail = text;
      }
    }
  } catch {
    detail = '';
  }
  return detail ? `${fallback}: ${detail}` : `${fallback} (${response.status})`;
}

export async function fetchEnvironments() {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/environments?limit=500&offset=0`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load environments'));
  }
  const payload = await response.json();
  return payload?.environments || [];
}

export async function getSliceEnvironment(sliceId) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/slices/${encodeURIComponent(sliceId)}/environment`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load slice environment'));
  }
  return response.json();
}

export async function updateSliceEnvironment(sliceId, environment) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/slices/${encodeURIComponent(sliceId)}/environment`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ environment }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to update slice environment'));
  }
  return response.json();
}

export async function createRevertChangeset(commitHash, sliceId = '') {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/commits/${encodeURIComponent(commitHash)}/changes/revert`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      sliceId: sliceId || undefined,
    }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to create revert changeset'));
  }
  return response.json();
}

export async function getChangesetDiff(changesetId, snapshotVersion) {
  const query = new URLSearchParams();
  if (typeof snapshotVersion === 'number' && snapshotVersion > 0) {
    query.set('snapshot_version', String(snapshotVersion));
  }
  const suffix = query.toString() ? `?${query.toString()}` : '';
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/changesets/${encodeURIComponent(changesetId)}/diff${suffix}`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load changeset diff'));
  }
  return response.json();
}

export async function listChangesetSnapshots(changesetId, limit = 100) {
  const query = new URLSearchParams();
  if (typeof limit === 'number' && limit > 0) {
    query.set('limit', String(limit));
  }
  const suffix = query.toString() ? `?${query.toString()}` : '';
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/changesets/${encodeURIComponent(changesetId)}/snapshots${suffix}`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load changeset snapshots'));
  }
  return response.json();
}

export async function mergeChangeset(changesetId) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/changesets/${encodeURIComponent(changesetId)}/merge`, {
    method: 'POST',
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to merge changeset'));
  }
  return response.json();
}

export async function closeChangeset(changesetId) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/changesets/${encodeURIComponent(changesetId)}/close`, {
    method: 'POST',
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to close changeset'));
  }
  return response.json();
}

export async function fetchAgentCapabilities() {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/agent-sessions/capabilities`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load agent capabilities'));
  }
  return response.json();
}

export async function createAgentSession(input) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/agent-sessions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      sliceId: input?.sliceId || '',
      environment: input?.environment || '',
      agentType: input?.agentType || '',
      idleTimeoutSec: input?.idleTimeoutSec || 1800,
      ttlSec: input?.ttlSec || 14400,
      env: input?.env || {},
    }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to create agent session'));
  }
  return response.json();
}

export async function getAgentSession(sessionId) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/agent-sessions/${encodeURIComponent(sessionId)}`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load agent session'));
  }
  return response.json();
}

export async function stopAgentSession(sessionId, reason = 'user_stop') {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/agent-sessions/${encodeURIComponent(sessionId)}/stop`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ reason }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to stop agent session'));
  }
  return response.json();
}

export async function mintAgentSessionToken(sessionId) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/agent-sessions/${encodeURIComponent(sessionId)}/token`, {
    method: 'POST',
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to mint agent session token'));
  }
  return response.json();
}
