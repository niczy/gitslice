// ---------------------------------------------------------------------------
// API and authentication helpers
// ---------------------------------------------------------------------------

import { getSignedInAuthSource, getSignedInUsername } from '../auth.js';
import { normalizeChangesetListResponse, normalizeCommitListResponse } from './normalize.js';

// Browser data requests stay same-origin so auth cookies continue to work
// when the web tier proxies API traffic to a different origin.
export const apiBaseUrl = '';

export function currentUsername() {
  return getSignedInUsername();
}

export function fetchWithAuth(url, options = {}) {
  const headers = new Headers(options.headers || {});
  const authSource = getSignedInAuthSource();
  const username = currentUsername();
  if (username && authSource !== 'clerk') {
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

export async function fetchRepoBindings() {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/repos/bindings`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load repo bindings'));
  }
  const payload = await response.json();
  return payload?.bindings || [];
}

export async function fetchCurrentUser() {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/users/me`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load profile'));
  }
  return response.json();
}

export async function updateCurrentUser({ name = '', primaryEmail = '' } = {}) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/users/me`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      name,
      primaryEmail,
    }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to update profile'));
  }
  return response.json();
}

export async function deleteCurrentUser() {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/users/me`, {
    method: 'DELETE',
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to delete account'));
  }
}

export async function fetchAuthSessions() {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/auth/sessions`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load sessions'));
  }
  const payload = await response.json();
  return payload?.sessions || [];
}

export async function fetchAuthContext() {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/auth/context`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load auth context'));
  }
  return response.json();
}

export async function fetchAdminStatus() {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/admin/status`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load admin status'));
  }
  return response.json();
}

export async function deleteAdminUserByEmail(email) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/admin/users:deleteByEmail`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to delete user'));
  }
  return response.json();
}

export async function deleteAuthSession(sessionId) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/auth/sessions/${encodeURIComponent(sessionId)}`, {
    method: 'DELETE',
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to revoke session'));
  }
}

export async function fetchAuthMethods() {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/auth/methods`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load auth methods'));
  }
  const payload = await response.json();
  return payload?.methods || [];
}

export async function deleteAuthMethod(methodId) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/auth/methods/${encodeURIComponent(methodId)}`, {
    method: 'DELETE',
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to remove auth method'));
  }
}

export async function fetchOrganizations() {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/orgs`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load organizations'));
  }
  const payload = await response.json();
  return payload?.organizations || [];
}

export async function createOrganization({ name = '' } = {}) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/orgs`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to create organization'));
  }
  return response.json();
}

export async function searchWorkspaceFiles(workspaceId, { query, glob = '', regex = false, signal = undefined } = {}) {
  const params = new URLSearchParams();
  params.set('query', String(query || '').trim());
  if (String(glob || '').trim()) {
    params.set('glob', String(glob || '').trim());
  }
  if (regex) {
    params.set('regex', 'true');
  }

  const response = await fetchWithAuth(`${apiBaseUrl}/v1/fs/workspaces/${encodeURIComponent(workspaceId)}:search?${params.toString()}`, { signal });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to search files'));
  }
  return response.json();
}

export async function createSliceFromFolder({
  parentSliceId = 'root_slice',
  folderPaths = [],
  newSliceId = '',
  name = '',
  description = '',
} = {}) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/slices:createFromFolder`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      parentSliceId,
      folderPaths,
      newSliceId,
      name,
      description,
    }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to create slice'));
  }
  const payload = await response.json();
  return {
    ...payload,
    slice_id: payload?.slice_id ?? payload?.sliceId ?? '',
  };
}

export async function fetchSliceEntries(sliceId, path = '') {
  const encodePath = (value) => String(value || '').split('/').map(encodeURIComponent).join('/');
  const encodedPath = path ? encodePath(path) : '';
  const pathSuffix = encodedPath ? `/${encodedPath}` : '';
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/slices/${encodeURIComponent(sliceId)}/entries${pathSuffix}`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load entries'));
  }
  const payload = await response.json();
  return payload?.entries || [];
}

function decodeBase64(value) {
  const binary = atob(value);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}

function encodeBase64(bytes) {
  let binary = '';
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary);
}

function normalizeAgentPublicKeyInput(input) {
  const trimmed = String(input || '').trim();
  if (!trimmed) {
    throw new Error('Public key is required.');
  }

  const rawBase64 = trimmed
    .replace('-----BEGIN PUBLIC KEY-----', '')
    .replace('-----END PUBLIC KEY-----', '')
    .replace(/\s+/g, '');
  const bytes = decodeBase64(rawBase64);
  if (bytes.length === 32) {
    return encodeBase64(bytes);
  }

  const expectedPrefix = [0x30, 0x2a, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x03, 0x21, 0x00];
  const matchesPrefix = bytes.length === expectedPrefix.length + 32 && expectedPrefix.every((value, index) => bytes[index] === value);
  if (matchesPrefix) {
    return encodeBase64(bytes.slice(expectedPrefix.length));
  }

  throw new Error('Unsupported public key format. Paste the .pub file generated by gs auth keygen or raw base64 ed25519 public key bytes.');
}

export async function fetchAgentKeys() {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/auth/agent/keys`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load agent keys'));
  }
  const payload = await response.json();
  return payload?.keys || [];
}

export async function createAgentKey({ name, publicKeyText }) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/auth/agent/keys`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      name,
      algorithm: 'ed25519',
      publicKey: normalizeAgentPublicKeyInput(publicKeyText),
    }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to add agent key'));
  }
  return response.json();
}

export async function revokeAgentKey(keyId) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/auth/agent/keys/${encodeURIComponent(keyId)}`, {
    method: 'DELETE',
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to revoke agent key'));
  }
}

export async function getSliceEnvironment(sliceId) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/slices/${encodeURIComponent(sliceId)}/environment`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load slice environment'));
  }
  return response.json();
}

export async function getSliceVisibility(sliceId) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/slices/${encodeURIComponent(sliceId)}/visibility`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load slice visibility'));
  }
  return response.json();
}

export async function updateSliceVisibility(sliceId, { visibility, pathPropagationMode }) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/slices/${encodeURIComponent(sliceId)}:setVisibility`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      visibility,
      pathPropagationMode,
    }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to update slice visibility'));
  }
  return response.json();
}

export async function getPathVisibility({ workspaceId, path }) {
  const params = new URLSearchParams({
    workspace_id: workspaceId,
    path,
  });
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/fs:visibility?${params.toString()}`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load path visibility'));
  }
  return response.json();
}

export async function updatePathVisibility({ path, visibility, recursive = false }) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/fs:visibility`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      path,
      visibility,
      recursive,
    }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to update path visibility'));
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

export async function listSliceCommits(sliceId, { limit = 100, fromCommitHash = '' } = {}) {
  const query = new URLSearchParams();
  if (typeof limit === 'number' && limit > 0) {
    query.set('limit', String(limit));
  }
  if (fromCommitHash) {
    query.set('from_commit_hash', fromCommitHash);
  }
  const suffix = query.toString() ? `?${query.toString()}` : '';
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/slices/${encodeURIComponent(sliceId)}/commits${suffix}`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load slice commits'));
  }
  return normalizeCommitListResponse(await response.json());
}

function changesetStatusQueryValue(statusFilter) {
  switch (statusFilter) {
    case 'pending':
      return '0';
    case 'approved':
      return '1';
    case 'rejected':
      return '2';
    case 'merged':
      return '3';
    default:
      return '';
  }
}

export async function listSliceChangesets(sliceId, { limit = 100, statusFilter = 'all' } = {}) {
  const query = new URLSearchParams();
  if (typeof limit === 'number' && limit > 0) {
    query.set('limit', String(limit));
  }
  const statusValue = changesetStatusQueryValue(statusFilter);
  if (statusValue) {
    query.set('status_filter', statusValue);
  } else {
    query.set('include_all_statuses', 'true');
  }
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/slices/${encodeURIComponent(sliceId)}/changesets?${query.toString()}`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load slice changesets'));
  }
  return normalizeChangesetListResponse(await response.json());
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
