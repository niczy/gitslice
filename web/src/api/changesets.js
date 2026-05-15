import { apiBaseUrl, fetchWithAuth, readErrorMessage } from './client.js';

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

export async function listChangesetSnapshots(changesetId, options = {}) {
  const normalizedOptions = typeof options === 'number' ? { limit: options } : options;
  const { limit = 100, omitModifiedFiles = true } = normalizedOptions || {};
  const query = new URLSearchParams();
  if (typeof limit === 'number' && limit > 0) {
    query.set('limit', String(limit));
  }
  if (omitModifiedFiles) {
    query.set('omit_modified_files', 'true');
  }
  const suffix = query.toString() ? `?${query.toString()}` : '';
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/changesets/${encodeURIComponent(changesetId)}/snapshots${suffix}`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load changeset snapshots'));
  }
  return response.json();
}

export async function getChangesetArtifactLinks(changesetId) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/changesets/${encodeURIComponent(changesetId)}/artifact-links`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load changeset links'));
  }
  return response.json();
}

export async function getCommitArtifactLinks(commitHash) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/commits/${encodeURIComponent(commitHash)}/artifact-links`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load commit links'));
  }
  return response.json();
}

export async function mergeChangeset(changesetId, { force = false, forceReason = '' } = {}) {
  const body = force || forceReason ? JSON.stringify({
    force,
    forceReason,
  }) : undefined;
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/changesets/${encodeURIComponent(changesetId)}/merge`, {
    method: 'POST',
    headers: body ? { 'Content-Type': 'application/json' } : undefined,
    body,
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
