export function listEntriesHasStateCursors(payload) {
  const stateToken = payload?.stateToken || payload?.state_token || null;
  const cursors = stateToken?.cursors || [];
  return Array.isArray(cursors) && cursors.length > 0;
}

export function getPinnedSliceHashFromListEntries(payload) {
  if (listEntriesHasStateCursors(payload)) {
    return '';
  }
  return String(payload?.sliceHash || payload?.slice_hash || '').trim();
}
