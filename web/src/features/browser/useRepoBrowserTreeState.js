import { useEffect, useLayoutEffect, useRef, useState } from 'react';

const useIsomorphicLayoutEffect = typeof window === 'undefined' ? useEffect : useLayoutEffect;

export function useRepoBrowserTreeState({
  initialBrowserData,
  initialDataMatchesRawSlice,
  initialSelectedDirectoryPath,
  initialSelectedFilePath,
}) {
  const [treeEntries, setTreeEntries] = useState(() => (
    initialDataMatchesRawSlice && Array.isArray(initialBrowserData?.rootEntries)
      ? { '': initialBrowserData.rootEntries }
      : {}
  ));
  const [expandedPaths, setExpandedPaths] = useState(['']);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState(() => (
    initialDataMatchesRawSlice ? initialBrowserData?.rootEntriesError || '' : ''
  ));
  const [focusedEntry, setFocusedEntry] = useState(() => (initialSelectedFilePath
    ? { path: initialSelectedFilePath, type: 'file' }
    : { path: initialSelectedDirectoryPath, type: 'directory' }));

  const treeEntriesRef = useRef(treeEntries);
  const treeEntriesScopeRef = useRef('');
  const hasMountedSliceRef = useRef(false);

  useIsomorphicLayoutEffect(() => {
    treeEntriesRef.current = treeEntries;
  }, [treeEntries]);

  return {
    error,
    expandedPaths,
    focusedEntry,
    hasLoadedRootEntries: Object.prototype.hasOwnProperty.call(treeEntries, ''),
    hasMountedSliceRef,
    isLoading,
    setError,
    setExpandedPaths,
    setFocusedEntry,
    setIsLoading,
    setTreeEntries,
    treeEntries,
    treeEntriesRef,
    treeEntriesScopeRef,
  };
}
