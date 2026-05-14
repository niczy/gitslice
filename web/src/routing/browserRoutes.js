export function parseBrowserState(search = '') {
  const params = new URLSearchParams(search || '');
  return {
    dir: params.get('dir') || '',
    file: params.get('file') || '',
    slice: params.get('slice') || '',
    sliceHash: params.get('sliceHash') || '',
    agentSession: params.get('session') || params.get('agentSession') || '',
  };
}

export function isSliceScopedRoute(page) {
  return page === 'browser' || page === 'slice-commits' || page === 'slice-changesets' || page === 'slice-agents';
}

export function buildBrowserPath(state = {}) {
  const params = new URLSearchParams();
  if (state.file) params.set('file', state.file);
  if (!state.file && state.dir) params.set('dir', state.dir);
  if (state.sliceHash) params.set('sliceHash', state.sliceHash);
  const query = params.toString();
  const slice = String(state.slice || '').trim();
  if (!slice) {
    return query ? `/slices?${query}` : '/slices';
  }
  const path = `/slices/${encodeURIComponent(slice)}`;
  return query ? `${path}?${query}` : path;
}

export function buildSliceAgentsPath(state = {}) {
  const params = new URLSearchParams();
  const sessionId = String(state.agentSession || state.session || '').trim();
  if (sessionId) params.set('session', sessionId);
  const query = params.toString();
  const path = `/slices/${encodeURIComponent(state.slice)}/agents`;
  return query ? `${path}?${query}` : path;
}
