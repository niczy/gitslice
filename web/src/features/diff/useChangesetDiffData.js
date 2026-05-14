import { useEffect, useRef, useState } from 'react';
import {
  getChangesetDiff,
  listChangesetSnapshots,
} from '../../utils/api.js';
import {
  normalizeChangesetDiffResponse,
  normalizeChangesetSnapshotListResponse,
} from '../../utils/normalize.js';

export function useChangesetSnapshotsData({
  changesetId,
  initialChangesetId = '',
  initialSnapshotVersion = 0,
  initialSnapshots = null,
  initialSnapshotsError = '',
}) {
  const hasInitialSnapshots = initialChangesetId === changesetId && Array.isArray(initialSnapshots);
  const initialSelectedSnapshotVersion = hasInitialSnapshots
    ? initialSnapshotVersion || initialSnapshots[0]?.version || 0
    : 0;
  const [snapshots, setSnapshots] = useState(() => (hasInitialSnapshots ? initialSnapshots : []));
  const [snapshotsLoaded, setSnapshotsLoaded] = useState(() => hasInitialSnapshots);
  const [loadedSnapshotsChangesetId, setLoadedSnapshotsChangesetId] = useState(() => (hasInitialSnapshots ? changesetId : ''));
  const [snapshotsError, setSnapshotsError] = useState(() => (hasInitialSnapshots ? initialSnapshotsError : ''));
  const [selectedSnapshotVersion, setSelectedSnapshotVersion] = useState(() => initialSelectedSnapshotVersion);
  const clientRefreshSnapshotsRef = useRef('');

  useEffect(() => {
    if (
      initialChangesetId === changesetId
      && Array.isArray(initialSnapshots)
      && loadedSnapshotsChangesetId !== changesetId
    ) {
      const nextVersion = initialSnapshotVersion || initialSnapshots[0]?.version || 0;
      setSnapshots(initialSnapshots);
      setSnapshotsLoaded(true);
      setSnapshotsError(initialSnapshotsError || '');
      setSelectedSnapshotVersion(nextVersion);
      setLoadedSnapshotsChangesetId(changesetId);
      return undefined;
    }
    if (!changesetId) {
      setSnapshots([]);
      setSnapshotsLoaded(false);
      setSnapshotsError('');
      setSelectedSnapshotVersion(0);
      setLoadedSnapshotsChangesetId('');
      return undefined;
    }
    if (loadedSnapshotsChangesetId === changesetId && clientRefreshSnapshotsRef.current === changesetId) {
      return undefined;
    }
    const hasSeededSnapshots = loadedSnapshotsChangesetId === changesetId && snapshotsLoaded;
    clientRefreshSnapshotsRef.current = changesetId;

    let active = true;
    const loadSnapshots = async () => {
      if (!hasSeededSnapshots) {
        setSnapshotsLoaded(false);
        setSnapshotsError('');
      }
      try {
        const response = await listChangesetSnapshots(changesetId);
        if (!active) {
          return;
        }
        const normalized = normalizeChangesetSnapshotListResponse(response);
        setSnapshots(normalized);
        setSelectedSnapshotVersion(normalized[0]?.version || 0);
        setLoadedSnapshotsChangesetId(changesetId);
      } catch (err) {
        if (!active) {
          return;
        }
        setSnapshots([]);
        setSelectedSnapshotVersion(0);
        setSnapshotsError(err?.message || 'Unable to load snapshot versions.');
        setLoadedSnapshotsChangesetId(changesetId);
      } finally {
        if (active) {
          setSnapshotsLoaded(true);
        }
      }
    };
    loadSnapshots();
    return () => { active = false; };
  }, [
    changesetId,
    initialChangesetId,
    initialSnapshotVersion,
    initialSnapshots,
    initialSnapshotsError,
    loadedSnapshotsChangesetId,
    snapshotsLoaded,
  ]);

  return {
    loadedSnapshotsChangesetId,
    selectedSnapshotVersion,
    setSelectedSnapshotVersion,
    snapshots,
    snapshotsError,
    snapshotsLoaded,
  };
}

export function useChangesetDiffData({
  changesetId,
  initialChangesetId = '',
  initialDiffData = null,
  initialDiffError = '',
  initialSnapshotVersion = 0,
  selectedSnapshotVersion = 0,
  snapshotsLoaded = false,
}) {
  const hasInitialDiff = initialChangesetId === changesetId && Boolean(initialDiffData);
  const initialSelectedSnapshotVersion = initialSnapshotVersion || 0;
  const [payload, setPayload] = useState(() => (hasInitialDiff ? initialDiffData : null));
  const [loadedDiffKey, setLoadedDiffKey] = useState(() => (
    hasInitialDiff ? `${changesetId}:${initialSelectedSnapshotVersion}` : ''
  ));
  const [isLoading, setIsLoading] = useState(() => !hasInitialDiff && !initialDiffError);
  const [error, setError] = useState(() => (initialChangesetId === changesetId ? initialDiffError : ''));
  const clientRefreshDiffRef = useRef('');

  useEffect(() => {
    const nextDiffKey = `${changesetId || ''}:${selectedSnapshotVersion || 0}`;
    if (
      initialChangesetId === changesetId
      && initialDiffData
      && nextDiffKey === `${changesetId || ''}:${initialSnapshotVersion || 0}`
      && loadedDiffKey !== nextDiffKey
    ) {
      setPayload(initialDiffData);
      setError('');
      setIsLoading(false);
      setLoadedDiffKey(nextDiffKey);
      return undefined;
    }
    if (!changesetId || !snapshotsLoaded) return undefined;
    if (loadedDiffKey === nextDiffKey && clientRefreshDiffRef.current === nextDiffKey) {
      return undefined;
    }
    const hasSeededDiff = loadedDiffKey === nextDiffKey && Boolean(payload);
    clientRefreshDiffRef.current = nextDiffKey;
    let active = true;
    const load = async () => {
      if (!hasSeededDiff) {
        setIsLoading(true);
        setError('');
      }
      try {
        const response = await getChangesetDiff(changesetId, selectedSnapshotVersion || undefined);
        if (active) {
          setPayload(normalizeChangesetDiffResponse(response));
          setLoadedDiffKey(nextDiffKey);
        }
      } catch (err) {
        if (active) {
          setError(err?.message || 'Unable to load changeset diff.');
          setLoadedDiffKey(nextDiffKey);
        }
      } finally {
        if (active) {
          setIsLoading(false);
        }
      }
    };
    load();
    return () => { active = false; };
  }, [
    changesetId,
    initialChangesetId,
    initialDiffData,
    initialSnapshotVersion,
    loadedDiffKey,
    payload,
    selectedSnapshotVersion,
    snapshotsLoaded,
  ]);

  return {
    error,
    isLoading,
    loadedDiffKey,
    payload,
  };
}
