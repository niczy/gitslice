export function payloadRequestId(payload) {
  return String(payload?.requestId || payload?.request_id || '').trim();
}

export function normalizeLocalChangePath(entry) {
  if (!entry) {
    return null;
  }
  const path = String(entry.path || '').trim();
  if (!path) {
    return null;
  }
  return {
    path,
    status: String(entry.status || '').trim().toUpperCase() || '?',
  };
}

export function normalizeLocalChangesPayload(payload = {}) {
  const rawChanges = payload?.changes || {};
  const paths = Array.isArray(payload?.paths)
    ? payload.paths.map(normalizeLocalChangePath).filter(Boolean)
    : [];
  const pathCount = Number(payload?.pathCount ?? payload?.path_count ?? paths.length);
  return {
    requestId: payloadRequestId(payload),
    status: String(payload?.status || '').trim(),
    workingTree: String(payload?.workingTree || payload?.working_tree || '').trim(),
    checkoutBase: String(payload?.checkoutBase || payload?.checkout_base || '').trim(),
    trackedChangesetId: String(payload?.trackedChangesetId || payload?.tracked_changeset_id || '').trim(),
    pathCount: Number.isFinite(pathCount) ? pathCount : paths.length,
    paths,
    truncated: Boolean(payload?.truncated),
    refreshedAt: payload?.refreshedAt || payload?.refreshed_at || '',
    changes: {
      added: Number(rawChanges.added || 0),
      modified: Number(rawChanges.modified || 0),
      deleted: Number(rawChanges.deleted || 0),
    },
  };
}

export function localChangesSummaryText(localChanges) {
  if (!localChanges) {
    return 'Not loaded';
  }
  if (localChanges.pathCount === 0) {
    return 'Clean';
  }
  const parts = [
    localChanges.changes.added ? `+${localChanges.changes.added}` : '',
    localChanges.changes.modified ? `~${localChanges.changes.modified}` : '',
    localChanges.changes.deleted ? `-${localChanges.changes.deleted}` : '',
  ].filter(Boolean);
  return parts.length ? parts.join(' ') : `${localChanges.pathCount} changed`;
}

export function changeStatusLabel(status) {
  switch (String(status || '').toUpperCase()) {
    case 'A':
      return 'Added';
    case 'M':
      return 'Modified';
    case 'D':
      return 'Deleted';
    default:
      return status || 'Changed';
  }
}
