function decodeSegment(value) {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

function normalizePathname(value) {
  const pathname = String(value || '').trim() || '/';
  if (pathname === '/') {
    return '/';
  }
  return pathname.replace(/\/+$/, '') || '/';
}

function parseBrowserState(search = '') {
  const params = new URLSearchParams(search || '');
  return {
    file: params.get('file') || '',
    slice: params.get('slice') || '',
    sliceHash: params.get('sliceHash') || '',
  };
}

function parseLegacyHash(rawHash) {
  const hash = String(rawHash || '').replace(/^#\/?/, '');
  if (hash.startsWith('diff/')) {
    return { page: 'diff', commitHash: decodeSegment(hash.slice(5)), changesetId: '' };
  }
  if (hash.startsWith('changesets/')) {
    return { page: 'changeset', commitHash: '', changesetId: decodeSegment(hash.slice(11)) };
  }
  if (hash === 'login') {
    return { page: 'login', commitHash: '', changesetId: '' };
  }
  if (hash === 'docs') {
    return { page: 'docs', commitHash: '', changesetId: '' };
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
  if (hash === 'admin') {
    return { page: 'admin', commitHash: '', changesetId: '' };
  }
  if (hash === 'browser' || hash.startsWith('browser?')) {
    const queryIndex = hash.indexOf('?');
    const search = queryIndex >= 0 ? hash.slice(queryIndex) : '';
    return {
      page: 'browser',
      commitHash: '',
      changesetId: '',
      browserState: parseBrowserState(search),
    };
  }
  if (hash === '' || hash === '/') {
    return { page: 'landing', commitHash: '', changesetId: '' };
  }
  return { page: 'not-found', commitHash: '', changesetId: '', unknownPath: hash };
}

export function parseLocation(locationLike = (typeof window !== 'undefined' ? window.location : { pathname: '/', search: '', hash: '' })) {
  const hash = String(locationLike?.hash || '').trim();
  if (hash.startsWith('#/')) {
    return {
      ...parseLegacyHash(hash),
      legacyHash: hash,
    };
  }

  const pathname = normalizePathname(locationLike?.pathname || '/');
  if (pathname === '/') {
    return { page: 'landing', commitHash: '', changesetId: '' };
  }
  if (pathname === '/login') {
    return { page: 'login', commitHash: '', changesetId: '' };
  }
  if (pathname === '/docs') {
    return { page: 'docs', commitHash: '', changesetId: '' };
  }
  if (pathname === '/profile' || pathname.startsWith('/profile/')) {
    return { page: 'profile', commitHash: '', changesetId: '' };
  }
  if (pathname === '/projects') {
    return { page: 'projects', commitHash: '', changesetId: '' };
  }
  if (pathname === '/settings') {
    return { page: 'settings', commitHash: '', changesetId: '' };
  }
  if (pathname === '/admin') {
    return { page: 'admin', commitHash: '', changesetId: '' };
  }
  if (pathname === '/browser') {
    return {
      page: 'browser',
      commitHash: '',
      changesetId: '',
      browserState: parseBrowserState(locationLike?.search || ''),
    };
  }
  if (pathname.startsWith('/diff/')) {
    return {
      page: 'diff',
      commitHash: decodeSegment(pathname.slice('/diff/'.length)),
      changesetId: '',
    };
  }
  if (pathname.startsWith('/changesets/')) {
    return {
      page: 'changeset',
      commitHash: '',
      changesetId: decodeSegment(pathname.slice('/changesets/'.length)),
    };
  }
  return {
    page: 'not-found',
    commitHash: '',
    changesetId: '',
    unknownPath: `${pathname}${locationLike?.search || ''}`,
  };
}

export function buildBrowserPath(state = {}) {
  const params = new URLSearchParams();
  if (state.file) params.set('file', state.file);
  if (state.slice) params.set('slice', state.slice);
  if (state.sliceHash) params.set('sliceHash', state.sliceHash);
  const query = params.toString();
  return query ? `/browser?${query}` : '/browser';
}

export function buildPath(page, commitHash, changesetId = '', browserState) {
  if (page === 'diff' && commitHash) {
    return `/diff/${encodeURIComponent(commitHash)}`;
  }
  if (page === 'changeset' && changesetId) {
    return `/changesets/${encodeURIComponent(changesetId)}`;
  }
  if (page === 'login') {
    return '/login';
  }
  if (page === 'docs') {
    return '/docs';
  }
  if (page === 'profile') {
    return '/profile';
  }
  if (page === 'projects') {
    return '/projects';
  }
  if (page === 'settings') {
    return '/settings';
  }
  if (page === 'admin') {
    return '/admin';
  }
  if (page === 'browser') {
    return buildBrowserPath(browserState);
  }
  return '/';
}

export function buildLegacyRedirectPath(locationLike = (typeof window !== 'undefined' ? window.location : { hash: '' })) {
  const parsed = parseLocation(locationLike);
  if (!parsed.legacyHash) {
    return '';
  }
  return buildPath(parsed.page, parsed.commitHash, parsed.changesetId, parsed.browserState);
}
