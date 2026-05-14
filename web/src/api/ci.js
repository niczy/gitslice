import { apiBaseUrl, fetchWithAuth, readErrorMessage } from './client.js';

function appendQueryNumber(query, key, value) {
  if (typeof value === 'number' && value > 0) {
    query.set(key, String(value));
  }
}

function appendQueryString(query, key, value) {
  const trimmed = String(value || '').trim();
  if (trimmed) {
    query.set(key, trimmed);
  }
}

export async function listCIRuns({ changesetId = '', status = '', limit = 50, pageToken = '' } = {}) {
  const query = new URLSearchParams();
  appendQueryString(query, 'changeset_id', changesetId);
  appendQueryString(query, 'status', status);
  appendQueryNumber(query, 'limit', limit);
  appendQueryString(query, 'page_token', pageToken);
  const suffix = query.toString() ? `?${query.toString()}` : '';
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/ci/runs${suffix}`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load CI runs'));
  }
  const payload = await response.json();
  return payload?.runs || [];
}

export async function listChangesetChecks(changesetId, changesetVersionId = '') {
  const query = new URLSearchParams();
  appendQueryString(query, 'changeset_version_id', changesetVersionId);
  const suffix = query.toString() ? `?${query.toString()}` : '';
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/changesets/${encodeURIComponent(changesetId)}/ci${suffix}`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load CI checks'));
  }
  const payload = await response.json();
  return payload?.checks || [];
}

export async function cancelCIRun(runId, reason = '') {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/ci/runs/${encodeURIComponent(runId)}:cancel`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ reason }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to cancel CI run'));
  }
  return response.json();
}

export async function rerunCI(runId, { failedOnly = false } = {}) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/ci/runs/${encodeURIComponent(runId)}:rerun`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ failedOnly }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to rerun CI'));
  }
  return response.json();
}

export async function listRunnerPools() {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/ci/runner-pools`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load runner pools'));
  }
  const payload = await response.json();
  return payload?.pools || [];
}

export async function listRunners({ pool = '', status = '', limit = 100, pageToken = '' } = {}) {
  const query = new URLSearchParams();
  appendQueryString(query, 'pool', pool);
  appendQueryString(query, 'status', status);
  appendQueryNumber(query, 'limit', limit);
  appendQueryString(query, 'page_token', pageToken);
  const suffix = query.toString() ? `?${query.toString()}` : '';
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/ci/runners${suffix}`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load runners'));
  }
  const payload = await response.json();
  return payload?.runners || [];
}

export async function getRunner(runnerId) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/ci/runners/${encodeURIComponent(runnerId)}`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load runner'));
  }
  return response.json();
}

export async function createRunnerToken({ name, pool = 'default', labels = [], ttl = '30m' }) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/ci/runner-tokens`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      name,
      pool,
      labels,
      ttl,
    }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to create runner token'));
  }
  return response.json();
}

export async function disableRunner(runnerId, reason = '') {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/ci/runners/${encodeURIComponent(runnerId)}:disable`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ reason }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to disable runner'));
  }
  return response.json();
}

export async function enableRunner(runnerId) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/ci/runners/${encodeURIComponent(runnerId)}:enable`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({}),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to enable runner'));
  }
  return response.json();
}

export async function revokeRunner(runnerId, { reason = '', requeueLeased = false, cancelLeased = false } = {}) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/ci/runners/${encodeURIComponent(runnerId)}:revoke`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      reason,
      requeueLeased,
      cancelLeased,
    }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to revoke runner'));
  }
  return response.json();
}

export async function listRunnerJobs(runnerId, limit = 20) {
  const query = new URLSearchParams();
  appendQueryNumber(query, 'limit', limit);
  const suffix = query.toString() ? `?${query.toString()}` : '';
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/ci/runners/${encodeURIComponent(runnerId)}/jobs${suffix}`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load runner jobs'));
  }
  const payload = await response.json();
  return payload?.jobs || [];
}

export async function listQueuedJobs({ pool = '', limit = 50 } = {}) {
  const query = new URLSearchParams();
  appendQueryString(query, 'pool', pool);
  appendQueryNumber(query, 'limit', limit);
  const suffix = query.toString() ? `?${query.toString()}` : '';
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/ci/queued-jobs${suffix}`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load queued CI jobs'));
  }
  const payload = await response.json();
  return payload?.jobs || [];
}
