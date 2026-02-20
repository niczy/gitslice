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
  return fetch(url, { ...options, headers });
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

export async function createRevertChangeset(commitHash, changeId, sliceId = '') {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/commits/${encodeURIComponent(commitHash)}/changes/revert`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      changeId,
      sliceId: sliceId || undefined,
    }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to create revert changeset'));
  }
  return response.json();
}

export async function getChangesetDiff(changesetId) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/changesets/${encodeURIComponent(changesetId)}/diff`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load changeset diff'));
  }
  return response.json();
}
