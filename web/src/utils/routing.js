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
    dir: params.get('dir') || '',
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
  if (pathname.startsWith('/browser/')) {
    const browserPath = pathname.slice('/browser/'.length);
    const [sliceSegment, viewSegment, ...extraSegments] = browserPath.split('/');
    const slice = decodeSegment(sliceSegment || '');
    if (slice && extraSegments.length === 0 && viewSegment === 'commits') {
      return {
        page: 'slice-commits',
        commitHash: '',
        changesetId: '',
        browserState: {
          ...parseBrowserState(locationLike?.search || ''),
          slice,
        },
      };
    }
    if (slice && extraSegments.length === 0 && viewSegment === 'changesets') {
      return {
        page: 'slice-changesets',
        commitHash: '',
        changesetId: '',
        browserState: {
          ...parseBrowserState(locationLike?.search || ''),
          slice,
        },
      };
    }
    return {
      page: 'browser',
      commitHash: '',
      changesetId: '',
      browserState: {
        ...parseBrowserState(locationLike?.search || ''),
        slice,
      },
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

export function resolveHomeRouteForUsername(routeInfo, username) {
  const user = String(username || '').trim();
  if (!user || routeInfo?.page !== 'landing') {
    return routeInfo;
  }
  return {
    ...routeInfo,
    page: 'browser',
    commitHash: '',
    changesetId: '',
    browserState: routeInfo?.browserState || {},
    resolvedHomeRoute: true,
  };
}

export function resolveHomeRouteForSession(routeInfo, session) {
  return resolveHomeRouteForUsername(routeInfo, session?.user?.username || '');
}

export function buildBrowserPath(state = {}) {
  const params = new URLSearchParams();
  if (state.file) params.set('file', state.file);
  if (!state.file && state.dir) params.set('dir', state.dir);
  if (state.sliceHash) params.set('sliceHash', state.sliceHash);
  const query = params.toString();
  const slice = String(state.slice || '').trim();
  if (!slice) {
    return query ? `/browser?${query}` : '/browser';
  }
  const path = `/browser/${encodeURIComponent(slice)}`;
  return query ? `${path}?${query}` : path;
}

export function buildPath(page, commitHash, changesetId = '', browserState) {
  if (page === 'diff' && commitHash) {
    return `/diff/${encodeURIComponent(commitHash)}`;
  }
  if (page === 'changeset' && changesetId) {
    return `/changesets/${encodeURIComponent(changesetId)}`;
  }
  if (page === 'slice-commits' && browserState?.slice) {
    return `/browser/${encodeURIComponent(browserState.slice)}/commits`;
  }
  if (page === 'slice-changesets' && browserState?.slice) {
    return `/browser/${encodeURIComponent(browserState.slice)}/changesets`;
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
