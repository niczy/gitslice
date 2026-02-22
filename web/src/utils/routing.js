// ---------------------------------------------------------------------------
// Hash-based routing helpers
// ---------------------------------------------------------------------------

function parseLoginRoute(hash) {
  if (hash === 'login') {
    return { page: 'login', commitHash: '', changesetId: '', redirectTo: '' };
  }
  if (hash.startsWith('login?')) {
    const params = new URLSearchParams(hash.slice(hash.indexOf('?') + 1));
    return {
      page: 'login',
      commitHash: '',
      changesetId: '',
      redirectTo: params.get('redirect') || '',
    };
  }
  return null;
}

export function parseHash() {
  const hash = window.location.hash.replace(/^#\/?/, '');
  if (!hash || hash === 'landing') {
    return { page: 'landing', commitHash: '', changesetId: '', redirectTo: '', unknownPath: '' };
  }

  const loginRoute = parseLoginRoute(hash);
  if (loginRoute) {
    return { ...loginRoute, unknownPath: '' };
  }

  if (hash.startsWith('diff/')) {
    return { page: 'diff', commitHash: decodeURIComponent(hash.slice(5)), changesetId: '', redirectTo: '', unknownPath: '' };
  }
  if (hash.startsWith('changesets/')) {
    return { page: 'changeset', commitHash: '', changesetId: decodeURIComponent(hash.slice(11)), redirectTo: '', unknownPath: '' };
  }
  if (hash === 'profile') {
    return { page: 'profile', commitHash: '', changesetId: '', redirectTo: '', unknownPath: '' };
  }
  if (hash === 'browser' || hash.startsWith('browser?')) {
    return { page: 'browser', commitHash: '', changesetId: '', redirectTo: '', unknownPath: '' };
  }
  if (hash === 'projects') {
    return { page: 'projects', commitHash: '', changesetId: '', redirectTo: '', unknownPath: '' };
  }
  if (hash === 'repos') {
    return { page: 'repos', commitHash: '', changesetId: '', redirectTo: '', unknownPath: '' };
  }
  if (hash === 'settings') {
    return { page: 'settings', commitHash: '', changesetId: '', redirectTo: '', unknownPath: '' };
  }
  if (hash === 'admin') {
    return { page: 'admin', commitHash: '', changesetId: '', redirectTo: '', unknownPath: '' };
  }

  return { page: 'not-found', commitHash: '', changesetId: '', redirectTo: '', unknownPath: hash };
}

export function buildHash(page, commitHash, changesetId = '', redirectTo = '') {
  if (page === 'diff' && commitHash) {
    return `#/diff/${encodeURIComponent(commitHash)}`;
  }
  if (page === 'changeset' && changesetId) {
    return `#/changesets/${encodeURIComponent(changesetId)}`;
  }
  if (page === 'login') {
    if (!redirectTo) {
      return '#/login';
    }
    return `#/login?redirect=${encodeURIComponent(redirectTo)}`;
  }
  if (page === 'profile') {
    return '#/profile';
  }
  if (page === 'browser') {
    return '#/browser';
  }
  if (page === 'projects') {
    return '#/projects';
  }
  if (page === 'repos') {
    return '#/repos';
  }
  if (page === 'settings') {
    return '#/settings';
  }
  if (page === 'admin') {
    return '#/admin';
  }
  return '#/';
}
