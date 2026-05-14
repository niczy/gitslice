import { useCallback, useEffect } from 'react';

import { parseLocation } from '../../utils/routing.js';
import { normalizeWorkspaceResultPath } from './browserApi.js';

export function useRepoBrowserRouteWriter({
  buildRoutePath,
  sliceHash,
  sliceId,
}) {
  return useCallback(({ file = '', dir = '' } = {}, options = {}) => {
    if (typeof window === 'undefined') {
      return;
    }
    const currentRoute = parseLocation(window.location);
    if (currentRoute.page !== 'browser' || !currentRoute.browserState?.slice) {
      return;
    }

    const nextPath = buildRoutePath({ dir, file });
    const currentPath = `${window.location.pathname}${window.location.search}`;
    if (currentPath === nextPath) {
      return;
    }

    const nextBrowserState = {
      dir,
      file,
      slice: sliceId,
      sliceHash,
    };
    const method = options.replace ? 'replaceState' : 'pushState';
    window.history[method]({
      gitsliceBrowserState: true,
      browserState: nextBrowserState,
    }, '', nextPath);
  }, [buildRoutePath, sliceHash, sliceId]);
}

export function useRepoBrowserRouteReader({
  isActive,
  openDirectoryPath,
  openFilePath,
  setSliceHash,
  sliceId,
}) {
  useEffect(() => {
    if (typeof window === 'undefined' || !isActive) {
      return undefined;
    }

    const handlePopState = () => {
      const route = parseLocation(window.location);
      if (route.page !== 'browser') {
        return;
      }
      const routeSliceId = route.browserState?.slice || '';
      if (routeSliceId && routeSliceId !== sliceId) {
        return;
      }

      setSliceHash(route.browserState?.sliceHash || '');
      const nextFile = normalizeWorkspaceResultPath(route.browserState?.file || '');
      const nextDir = normalizeWorkspaceResultPath(route.browserState?.dir || '');
      if (nextFile) {
        openFilePath(nextFile, { updateHistory: false });
        return;
      }
      openDirectoryPath(nextDir, { updateHistory: false });
    };

    window.addEventListener('popstate', handlePopState);
    return () => window.removeEventListener('popstate', handlePopState);
  }, [isActive, openDirectoryPath, openFilePath, setSliceHash, sliceId]);
}
