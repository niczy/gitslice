import { useCallback, useEffect, useMemo, useRef } from 'react';

import { buildBrowserPath, parseLocation } from '../../utils/routing.js';
import { getSliceDisplayName } from '../../utils/slices.js';

export function useInitialBrowserState() {
  return useMemo(() => {
    if (typeof window === 'undefined') {
      return null;
    }
    const route = parseLocation(window.location);
    return route.page === 'browser' ? route.browserState || null : null;
  }, []);
}

export function useRepoBrowserSlice({
  authUsername,
  currentSliceId,
  initialBrowserData,
  initialBrowserState,
  onSliceChange,
  sliceHash,
  slices,
  slicesLoading,
}) {
  const hasAppliedInitialSliceRef = useRef(false);
  const rawSliceId = currentSliceId;

  const resolveRequestedSliceId = useCallback((requestedSliceId) => {
    const requested = String(requestedSliceId || '').trim();
    if (!requested) {
      return '';
    }

    const normalizedAuthUsername = String(authUsername || '').trim().toLowerCase();
    const normalizedRequested = requested.toLowerCase();
    if (
      normalizedAuthUsername &&
      (normalizedRequested === normalizedAuthUsername || normalizedRequested === `home_${normalizedAuthUsername}`)
    ) {
      return `home_${normalizedAuthUsername}`;
    }

    const candidateIds = [requested];
    if (requested.startsWith('home_')) {
      const suffix = requested.slice('home_'.length).trim();
      if (suffix) {
        candidateIds.push(`home_${suffix.toLowerCase()}`);
      }
    } else {
      candidateIds.push(`home_${requested.toLowerCase()}`);
    }

    for (const candidateId of candidateIds) {
      if (slices.some((slice) => slice.slice_id === candidateId)) {
        return candidateId;
      }
    }

    return '';
  }, [authUsername, slices]);

  const sliceId = useMemo(() => {
    if (!rawSliceId) {
      return '';
    }
    return resolveRequestedSliceId(rawSliceId) || rawSliceId;
  }, [rawSliceId, resolveRequestedSliceId]);

  const treeEntriesScopeKey = useMemo(() => `${sliceId}\0${String(sliceHash || '')}`, [sliceHash, sliceId]);

  const initialBrowserDataMatches = useMemo(() => {
    return initialBrowserData?.selectedSliceId === sliceId
      && String(initialBrowserData?.sliceHash || '') === String(sliceHash || '');
  }, [initialBrowserData, sliceHash, sliceId]);

  useEffect(() => {
    if (!rawSliceId || sliceId === rawSliceId) {
      return;
    }
    const normalizedAuthUsername = String(authUsername || '').trim().toLowerCase();
    const normalizedRawSliceId = String(rawSliceId || '').trim().toLowerCase();
    const normalizedSliceId = String(sliceId || '').trim().toLowerCase();
    if (
      normalizedAuthUsername
      && normalizedRawSliceId === normalizedAuthUsername
      && normalizedSliceId === `home_${normalizedAuthUsername}`
    ) {
      return;
    }
    onSliceChange(sliceId);
  }, [authUsername, onSliceChange, rawSliceId, sliceId]);

  useEffect(() => {
    if (hasAppliedInitialSliceRef.current) {
      return;
    }
    if (slicesLoading) {
      return;
    }

    if (!initialBrowserState?.slice) {
      hasAppliedInitialSliceRef.current = true;
      return;
    }

    const resolvedSliceId = resolveRequestedSliceId(initialBrowserState.slice);
    if (!resolvedSliceId) {
      hasAppliedInitialSliceRef.current = true;
      return;
    }

    if (resolvedSliceId !== currentSliceId) {
      onSliceChange(resolvedSliceId);
    }
    hasAppliedInitialSliceRef.current = true;
  }, [currentSliceId, initialBrowserState, onSliceChange, resolveRequestedSliceId, slicesLoading]);

  const currentSlice = useMemo(() => {
    return slices.find((slice) => slice.slice_id === sliceId) || null;
  }, [slices, sliceId]);

  const canLoad = sliceId !== '' && (sliceId === 'root' || Boolean(String(authUsername || '').trim()));

  const currentSliceLabel = useMemo(() => {
    if (currentSlice?.name) {
      return currentSlice.name;
    }
    return sliceId === 'root' ? 'Root Slice' : sliceId;
  }, [currentSlice, sliceId]);

  const currentSliceDisplayName = useMemo(() => {
    return getSliceDisplayName(currentSliceLabel);
  }, [currentSliceLabel]);

  const canShowSettings = canLoad && !currentSlice?.is_root;

  const buildRoutePath = useCallback(({ dir = '', file = '' } = {}) => {
    return buildBrowserPath({
      dir,
      file,
      slice: sliceId,
      sliceHash,
    });
  }, [sliceHash, sliceId]);

  return {
    buildRoutePath,
    canLoad,
    canShowSettings,
    currentSlice,
    currentSliceDisplayName,
    currentSliceLabel,
    initialBrowserDataMatches,
    sliceId,
    treeEntriesScopeKey,
  };
}
