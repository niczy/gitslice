// ---------------------------------------------------------------------------
// Data normalization utilities
// ---------------------------------------------------------------------------

export function normalizeChange(c) {
  return {
    ...c,
    change_type: c.change_type ?? c.changeType,
    commit_hash: c.commit_hash ?? c.commitHash,
    lines_added: c.lines_added ?? c.linesAdded ?? 0,
    lines_deleted: c.lines_deleted ?? c.linesDeleted ?? 0,
    old_path: c.old_path ?? c.oldPath,
    slice_id: c.slice_id ?? c.sliceId,
    patch: c.patch ?? c.Patch ?? '',
  };
}

export function normalizeDiffResponse(data) {
  return {
    ...data,
    commit_hash: data.commit_hash ?? data.commitHash,
    files_added: data.files_added ?? data.filesAdded ?? 0,
    files_modified: data.files_modified ?? data.filesModified ?? 0,
    files_deleted: data.files_deleted ?? data.filesDeleted ?? 0,
    files_renamed: data.files_renamed ?? data.filesRenamed ?? 0,
    changes: (data.changes || []).map(normalizeChange),
  };
}

export function normalizeChangesetDiffResponse(data) {
  const changeset = data?.changeset || {};
  const diff = data?.diff || {};
  const snapshot = data?.snapshot || {};
  const normalizedChangeset = {
    ...changeset,
    changeset_id: changeset.changeset_id ?? changeset.changesetId,
    changeset_hash: changeset.changeset_hash ?? changeset.changesetHash,
    slice_id: changeset.slice_id ?? changeset.sliceId,
    base_commit_hash: changeset.base_commit_hash ?? changeset.baseCommitHash,
    modified_files: changeset.modified_files ?? changeset.modifiedFiles ?? [],
    created_at: changeset.created_at ?? changeset.createdAt,
    merged_at: changeset.merged_at ?? changeset.mergedAt,
    status: normalizeChangesetStatus(changeset.status),
  };
  const normalizedChanges = (data?.changes || [])
    .map(normalizeChange);
  const synthesizedChanges = (normalizedChangeset.modified_files || []).map((path, index) => ({
    id: `${normalizedChangeset.changeset_id || 'changeset'}-${index}`,
    slice_id: normalizedChangeset.slice_id || '',
    path,
    old_path: '',
    change_type: 'modify',
    lines_added: 0,
    lines_deleted: 0,
    patch: '',
  }));
  return {
    ...data,
    changeset: normalizedChangeset,
    snapshot: normalizeChangesetSnapshot(snapshot),
    diff: {
      ...diff,
      files_added: diff.files_added ?? diff.filesAdded ?? 0,
      files_modified: diff.files_modified ?? diff.filesModified ?? 0,
      files_deleted: diff.files_deleted ?? diff.filesDeleted ?? 0,
      lines_added: diff.lines_added ?? diff.linesAdded ?? 0,
      lines_removed: diff.lines_removed ?? diff.linesRemoved ?? 0,
    },
    changes: normalizedChanges.length > 0 ? normalizedChanges : synthesizedChanges,
  };
}

export function normalizeChangesetSnapshot(snapshot) {
  return {
    ...snapshot,
    snapshot_id: snapshot?.snapshot_id ?? snapshot?.snapshotId ?? '',
    changeset_id: snapshot?.changeset_id ?? snapshot?.changesetId ?? '',
    version: Number(snapshot?.version ?? 0),
    hash: snapshot?.hash ?? '',
    base_commit_hash: snapshot?.base_commit_hash ?? snapshot?.baseCommitHash ?? '',
    modified_files: snapshot?.modified_files ?? snapshot?.modifiedFiles ?? [],
    author: snapshot?.author ?? '',
    message: snapshot?.message ?? '',
    created_at: snapshot?.created_at ?? snapshot?.createdAt ?? 0,
  };
}

export function normalizeChangesetSnapshotListResponse(data) {
  const snapshots = data?.snapshots || [];
  return snapshots.map((snapshot) => normalizeChangesetSnapshot(snapshot));
}

export function normalizeChangesetStatus(value) {
  if (value === 0 || value === 'PENDING' || value === 'pending') return 'pending';
  if (value === 1 || value === 'APPROVED' || value === 'approved') return 'approved';
  if (value === 2 || value === 'REJECTED' || value === 'rejected') return 'rejected';
  if (value === 3 || value === 'MERGED' || value === 'merged') return 'merged';
  return 'pending';
}

export function normalizeSliceInfo(slice) {
  return {
    ...slice,
    slice_id: slice.slice_id ?? slice.sliceId,
    file_count: slice.file_count ?? slice.fileCount,
    is_root: slice.is_root ?? slice.isRoot ?? false,
  };
}

export function normalizeChangeType(value) {
  if (value === 1 || value === 'CHANGE_TYPE_ADD' || value === 'ADD' || value === 'add') {
    return 'add';
  }
  if (value === 2 || value === 'CHANGE_TYPE_MODIFY' || value === 'MODIFY' || value === 'modify') {
    return 'modify';
  }
  if (value === 3 || value === 'CHANGE_TYPE_DELETE' || value === 'DELETE' || value === 'delete') {
    return 'delete';
  }
  if (value === 4 || value === 'CHANGE_TYPE_RENAME' || value === 'RENAME' || value === 'rename') {
    return 'rename';
  }
  return 'unknown';
}

export function normalizeEntryType(value) {
  if (value === 2 || value === 'ENTRY_TYPE_DIRECTORY' || value === 'DIRECTORY') {
    return 'directory';
  }
  if (value === 1 || value === 'ENTRY_TYPE_FILE' || value === 'FILE') {
    return 'file';
  }
  return 'file';
}
