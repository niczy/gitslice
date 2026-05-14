import { useEffect, useRef, useState } from 'react';
import { apiBaseUrl, fetchWithAuth } from '../../utils/api.js';
import { normalizeDiffResponse } from '../../utils/normalize.js';

export function useCommitDiffData({
  commitHash,
  initialCommitHash = '',
  initialDiffData = null,
  initialDiffError = '',
}) {
  const hasInitialDiff = initialCommitHash === commitHash && Boolean(initialDiffData);
  const [diffData, setDiffData] = useState(() => (hasInitialDiff ? initialDiffData : null));
  const [loadedCommitHash, setLoadedCommitHash] = useState(() => (hasInitialDiff ? commitHash : ''));
  const [isLoading, setIsLoading] = useState(() => !hasInitialDiff && !initialDiffError);
  const [error, setError] = useState(() => (initialCommitHash === commitHash ? initialDiffError : ''));
  const [dataRevision, setDataRevision] = useState(0);
  const clientRefreshCommitRef = useRef('');

  useEffect(() => {
    if (initialCommitHash === commitHash && initialDiffData && loadedCommitHash !== commitHash) {
      setDiffData(initialDiffData);
      setError('');
      setIsLoading(false);
      setLoadedCommitHash(commitHash);
      setDataRevision((revision) => revision + 1);
      return undefined;
    }
    if (!commitHash) return undefined;
    if (loadedCommitHash === commitHash && clientRefreshCommitRef.current === commitHash) {
      return undefined;
    }
    const hasSeededDiff = loadedCommitHash === commitHash && Boolean(diffData);
    clientRefreshCommitRef.current = commitHash;
    let active = true;
    const controller = new AbortController();

    const loadDiff = async () => {
      if (!hasSeededDiff) {
        setIsLoading(true);
        setError('');
      }
      try {
        const response = await fetchWithAuth(`${apiBaseUrl}/v1/commits/${encodeURIComponent(commitHash)}/changes`, {
          signal: controller.signal,
        });
        if (!response.ok) throw new Error(`Request failed (${response.status})`);
        const payload = await response.json();
        if (active) {
          setDiffData(normalizeDiffResponse(payload));
          setLoadedCommitHash(commitHash);
          setDataRevision((revision) => revision + 1);
        }
      } catch (err) {
        if (active && err?.name !== 'AbortError') {
          setError('Unable to load commit changes.');
          setLoadedCommitHash(commitHash);
        }
      } finally {
        if (active) setIsLoading(false);
      }
    };

    loadDiff();
    return () => { active = false; controller.abort(); };
  }, [commitHash, diffData, initialCommitHash, initialDiffData, loadedCommitHash]);

  return {
    dataRevision,
    diffData,
    error,
    isLoading,
    loadedCommitHash,
  };
}
