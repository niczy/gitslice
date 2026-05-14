import { buildPath } from './pathBuilders.js';
import { isSliceScopedRoute, parseBrowserState } from './browserRoutes.js';
import { decodeSegment, normalizePathname } from './urlSegments.js';

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
  if (hash === 'settings/ci' || hash === 'settings/ci/runners' || hash === 'settings/ci/runs') {
    return { page: 'settings', commitHash: '', changesetId: '', settingsSection: hash.slice('settings/'.length) || 'account' };
  }
  if (hash.startsWith('settings/ci/runners/')) {
    return {
      page: 'settings',
      commitHash: '',
      changesetId: '',
      settingsSection: 'ci/runners',
      settingsRunnerId: decodeSegment(hash.slice('settings/ci/runners/'.length)),
    };
  }
  if (hash === 'admin') {
    return { page: 'admin', commitHash: '', changesetId: '' };
  }
  if (hash === 'browser' || hash.startsWith('browser?') || hash === 'slices' || hash.startsWith('slices?')) {
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
  if (pathname === '/settings/ci' || pathname === '/settings/ci/runners' || pathname === '/settings/ci/runs') {
    return {
      page: 'settings',
      commitHash: '',
      changesetId: '',
      settingsSection: pathname.slice('/settings/'.length) || 'account',
    };
  }
  if (pathname.startsWith('/settings/ci/runners/')) {
    return {
      page: 'settings',
      commitHash: '',
      changesetId: '',
      settingsSection: 'ci/runners',
      settingsRunnerId: decodeSegment(pathname.slice('/settings/ci/runners/'.length)),
    };
  }
  if (pathname === '/admin') {
    return { page: 'admin', commitHash: '', changesetId: '' };
  }
  if (pathname === '/browser' || pathname === '/slices') {
    return {
      page: 'browser',
      commitHash: '',
      changesetId: '',
      browserState: parseBrowserState(locationLike?.search || ''),
    };
  }
  if (pathname.startsWith('/browser/') || pathname.startsWith('/slices/')) {
    const prefix = pathname.startsWith('/slices/') ? '/slices/' : '/browser/';
    const browserPath = pathname.slice(prefix.length);
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
    if (slice && extraSegments.length === 0 && viewSegment === 'agents') {
      return {
        page: 'slice-agents',
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
  if (!user) {
    return routeInfo;
  }
  const normalizedUser = user.toLowerCase();
  const requestedSlice = String(routeInfo?.browserState?.slice || '').trim();
  const normalizedRequestedSlice = requestedSlice.toLowerCase();
  if (
    isSliceScopedRoute(routeInfo?.page)
    && requestedSlice
    && (normalizedRequestedSlice === normalizedUser || normalizedRequestedSlice === `home_${normalizedUser}`)
  ) {
    return {
      ...routeInfo,
      browserState: {
        ...(routeInfo?.browserState || {}),
        slice: `home_${normalizedUser}`,
      },
    };
  }
  if (routeInfo?.page !== 'landing') {
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

export function buildLegacyRedirectPath(locationLike = (typeof window !== 'undefined' ? window.location : { hash: '' })) {
  const parsed = parseLocation(locationLike);
  if (!parsed.legacyHash) {
    return '';
  }
  return buildPath(parsed.page, parsed.commitHash, parsed.changesetId, parsed.browserState);
}
