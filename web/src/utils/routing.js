// ---------------------------------------------------------------------------
// Hash-based routing helpers
// ---------------------------------------------------------------------------

export function parseHash() {
  const hash = window.location.hash.replace(/^#\/?/, '');
  if (hash.startsWith('diff/')) {
    return { page: 'diff', commitHash: decodeURIComponent(hash.slice(5)), changesetId: '' };
  }
  if (hash.startsWith('changesets/')) {
    return { page: 'changeset', commitHash: '', changesetId: decodeURIComponent(hash.slice(11)) };
  }
  if (hash === 'login') {
    return { page: 'login', commitHash: '', changesetId: '' };
  }
  if (hash === 'profile') {
    return { page: 'profile', commitHash: '', changesetId: '' };
  }
  if (hash === 'projects') {
    return { page: 'projects', commitHash: '', changesetId: '' };
  }
  if (hash === 'settings') {
    return { page: 'settings', commitHash: '', changesetId: '' };
  }
  if (hash === 'browser' || hash.startsWith('browser?')) {
    return { page: 'browser', commitHash: '', changesetId: '' };
  }
  if (hash === '' || hash === '/') {
    return { page: 'landing', commitHash: '', changesetId: '' };
  }
  return { page: 'not-found', commitHash: '', changesetId: '', unknownPath: hash };
}

export function buildHash(page, commitHash, changesetId = '') {
  if (page === 'diff' && commitHash) {
    return `#/diff/${encodeURIComponent(commitHash)}`;
  }
  if (page === 'changeset' && changesetId) {
    return `#/changesets/${encodeURIComponent(changesetId)}`;
  }
  if (page === 'login') {
    return '#/login';
  }
  if (page === 'profile') {
    return '#/profile';
  }
  if (page === 'projects') {
    return '#/projects';
  }
  if (page === 'settings') {
    return '#/settings';
  }
  if (page === 'browser') {
    return '#/browser';
  }
  return '#/';
}
