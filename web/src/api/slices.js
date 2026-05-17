import { apiBaseUrl, fetchWithAuth, readErrorMessage } from './client.js';
import { normalizeChangesetListResponse, normalizeCommitListResponse } from '../utils/normalize.js';

export async function searchSliceFiles(sliceId, { query, glob = '', regex = false, signal = undefined } = {}) {
  const params = new URLSearchParams();
  params.set('query', String(query || '').trim());
  if (String(glob || '').trim()) {
    params.set('glob', String(glob || '').trim());
  }
  if (regex) {
    params.set('regex', 'true');
  }

  const response = await fetchWithAuth(`${apiBaseUrl}/v1/fs/workspaces/${encodeURIComponent(sliceId)}:search?${params.toString()}`, { signal });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to search files'));
  }
  return response.json();
}

export async function createSliceFromFolder({
  parentSliceId = '',
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

export async function getSliceVisibility(sliceId) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/slices/${encodeURIComponent(sliceId)}/visibility`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load slice visibility'));
  }
  return response.json();
}

export async function updateSliceVisibility(sliceId, { visibility }) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/slices/${encodeURIComponent(sliceId)}:setVisibility`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      visibility,
    }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to update slice visibility'));
  }
  return response.json();
}

export async function addSliceFolder(sliceId, folderPath) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/slices/${encodeURIComponent(sliceId)}/folders:add`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ folderPath }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to add tracked folder'));
  }
  return response.json();
}

export async function removeSliceFolder(sliceId, folderPath) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/slices/${encodeURIComponent(sliceId)}/folders:remove`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ folderPath }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to remove tracked folder'));
  }
  return response.json();
}

export async function getSliceEnvRequirements(sliceId) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/slices/${encodeURIComponent(sliceId)}/env/requirements`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load environment requirements'));
  }
  return response.json();
}

export async function listSliceEnvKV(sliceId, { profile = 'local' } = {}) {
  const query = new URLSearchParams();
  query.set('profile', profile || 'default');
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/slices/${encodeURIComponent(sliceId)}/env/kv?${query.toString()}`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load environment KV entries'));
  }
  return response.json();
}

export async function setSliceEnvValue(sliceId, { profile = 'local', key, value } = {}) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/slices/${encodeURIComponent(sliceId)}/env/kv/values/${encodeURIComponent(key)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      profile,
      value,
    }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to save environment value'));
  }
  return response.json();
}

export async function setSliceEnvSecret(sliceId, { profile = 'local', key, value } = {}) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/slices/${encodeURIComponent(sliceId)}/env/kv/secrets/${encodeURIComponent(key)}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      profile,
      value,
    }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to save environment secret'));
  }
  return response.json();
}

export async function deleteSliceEnvKV(sliceId, {
  profile = 'local',
  className,
  key,
} = {}) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/slices/${encodeURIComponent(sliceId)}/env/kv/${encodeURIComponent(className)}/${encodeURIComponent(key)}?profile=${encodeURIComponent(profile)}`, {
    method: 'DELETE',
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to delete environment KV entry'));
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

export async function createChangeset({
  sliceId,
  baseCommitHash = '',
  modifiedFiles = [],
  message = '',
  changesetId = '',
  fileContents = [],
  expectedPathBases = [],
  fileRenames = [],
  directoryRenames = [],
} = {}) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/changesets`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      sliceId,
      baseCommitHash,
      modifiedFiles,
      message,
      changesetId,
      fileContents,
      expectedPathBases,
      fileRenames,
      directoryRenames,
    }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to create changeset'));
  }
  return response.json();
}

export async function createAndMergeChangeset({
  sliceId,
  baseCommitHash = '',
  modifiedFiles = [],
  message = '',
  fileContents = [],
  expectedPathBases = [],
  fileRenames = [],
  directoryRenames = [],
} = {}) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/changesets:createAndMerge`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      sliceId,
      baseCommitHash,
      modifiedFiles,
      message,
      fileContents,
      expectedPathBases,
      fileRenames,
      directoryRenames,
    }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to commit file tree change'));
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
  query.set('omit_modified_files', 'true');
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/slices/${encodeURIComponent(sliceId)}/changesets?${query.toString()}`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load slice changesets'));
  }
  return normalizeChangesetListResponse(await response.json());
}
