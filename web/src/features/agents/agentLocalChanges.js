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
  const linesAdded = Number(entry.linesAdded ?? entry.lines_added ?? 0);
  const linesDeleted = Number(entry.linesDeleted ?? entry.lines_deleted ?? 0);
  const metadataNotes = Array.isArray(entry.metadataNotes || entry.metadata_notes)
    ? (entry.metadataNotes || entry.metadata_notes)
      .map((note) => String(note || '').trim())
      .filter(Boolean)
    : [];
  return {
    path,
    status: String(entry.status || '').trim().toUpperCase() || '?',
    patch: String(entry.patch || ''),
    linesAdded: Number.isFinite(linesAdded) ? linesAdded : 0,
    linesDeleted: Number.isFinite(linesDeleted) ? linesDeleted : 0,
    binary: Boolean(entry.binary),
    metadataNotes,
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
    sessionId: String(payload?.sessionId || payload?.session_id || '').trim(),
    sliceId: String(payload?.sliceId || payload?.slice_id || '').trim(),
    checkoutDir: String(payload?.checkoutDir || payload?.checkout_dir || '').trim(),
    diffsIncluded: Boolean(payload?.diffsIncluded || payload?.diffs_included),
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

export function localChangeStateText(entry) {
  if (!entry) {
    return 'Changed';
  }
  const state = changeStatusLabel(entry.status);
  const parts = [];
  if (entry.linesAdded > 0) {
    parts.push(`+${entry.linesAdded}`);
  }
  if (entry.linesDeleted > 0) {
    parts.push(`-${entry.linesDeleted}`);
  }
  if (entry.binary) {
    parts.push('binary');
  }
  if (entry.metadataNotes?.length > 0) {
    parts.push('metadata');
  }
  return parts.length ? `${state} ${parts.join(' ')}` : state;
}
