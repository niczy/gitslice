// ---------------------------------------------------------------------------
// Hash-based routing helpers
// ---------------------------------------------------------------------------

export function parseHash() {
  const hash = window.location.hash.replace(/^#\/?/, '');
  if (hash.startsWith('diff/')) {
    return { page: 'diff', commitHash: decodeURIComponent(hash.slice(5)) };
  }
  if (hash === 'login') {
    return { page: 'login', commitHash: '' };
  }
  if (hash === 'profile') {
    return { page: 'profile', commitHash: '' };
  }
  if (hash === 'browser' || hash.startsWith('browser?')) {
    return { page: 'browser', commitHash: '' };
  }
  return { page: 'landing', commitHash: '' };
}

export function buildHash(page, commitHash) {
  if (page === 'diff' && commitHash) {
    return `#/diff/${encodeURIComponent(commitHash)}`;
  }
  if (page === 'login') {
    return '#/login';
  }
  if (page === 'profile') {
    return '#/profile';
  }
  if (page === 'browser') {
    return '#/browser';
  }
  return '#/';
}
