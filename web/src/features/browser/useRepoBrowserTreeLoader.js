import { useCallback, useEffect } from 'react';

import { fetchWithAuth } from '../../utils/api.js';
import { decodeBase64 } from '../../utils/highlight.js';
import {
  normalizeWorkspaceResultPath,
  readBrowserErrorMessage,
} from './browserApi.js';
import {
  getDirectoryAncestorPaths,
  getFilePayloadSize,
  getParentDirectoryPath,
  getTreeFileSize,
} from './browserModel.js';

export function useRepoBrowserTreeLoader({
  buildEntriesUrl,
  buildFileUrl,
  canLoad,
  clearFilePreview,
  expandedPaths,
  hasLoadedRootEntries,
  initialBrowserData,
  initialBrowserDataMatches,
  initialBrowserState,
  isActive,
  openFilesView,
  pendingFileRef,
  refreshHistoryToken,
  selectedDirectoryPath,
  selectedFile,
  selectedFileRef,
  setEncodedFileContent,
  setError,
  setExpandedPaths,
  setFileContent,
  setFileError,
  setFocusedEntry,
  setIsLoading,
  setLoadingFilePath,
  setPreviewEncodedFileContent,
  setPreviewFileContent,
  setPreviewFilePath,
  setSelectedFile,
  setSelectedFileSize,
  setTreeEntries,
  sliceHash,
  sliceId,
  treeEntries,
  treeEntriesRef,
  treeEntriesScopeKey,
  writeBrowserState,
}) {
  useEffect(() => {
    if (!isActive) {
      return undefined;
    }
    if (!canLoad) {
      return undefined;
    }
    if (initialBrowserDataMatches && Array.isArray(initialBrowserData?.rootEntries) && hasLoadedRootEntries) {
      return undefined;
    }

    let active = true;
    const controller = new AbortController();

    const loadRoot = async () => {
      setIsLoading(true);
      setError('');
      setFileError('');

      try {
        const response = await fetchWithAuth(buildEntriesUrl(''), {
          signal: controller.signal,
        });
        if (!response.ok) {
          throw new Error(`Request failed (${response.status})`);
        }
        const payload = await response.json();
        if (!active) {
          return;
        }

        const rootEntries = payload.entries || [];
        const pendingFile = pendingFileRef.current;
        const shouldRestorePendingFile = !initialBrowserState?.slice || initialBrowserState.slice === sliceId;
        if (pendingFile && shouldRestorePendingFile) {
          pendingFileRef.current = null;

          const parts = pendingFile.split('/');
          const allEntries = { '': rootEntries };
          const pathsToExpand = [''];

          for (let i = 1; i < parts.length; i += 1) {
            if (!active) return;
            const parentPath = parts.slice(0, i).join('/');
            pathsToExpand.push(parentPath);
            try {
              const dirResp = await fetchWithAuth(buildEntriesUrl(parentPath), { signal: controller.signal });
              if (dirResp.ok) {
                const dirData = await dirResp.json();
                allEntries[parentPath] = dirData.entries || [];
              }
            } catch (err) {
              if (err?.name === 'AbortError') return;
              break;
            }
          }

          if (!active) return;
          setTreeEntries(allEntries);
          setExpandedPaths(pathsToExpand);
          setSelectedFile(pendingFile);
          setFocusedEntry({ path: pendingFile, type: 'file' });
          setSelectedFileSize(getTreeFileSize(allEntries, pendingFile));
          setLoadingFilePath(pendingFile);

          try {
            const fileResp = await fetchWithAuth(buildFileUrl(pendingFile), { signal: controller.signal });
            if (!fileResp.ok) {
              throw new Error(await readBrowserErrorMessage(fileResp, 'Unable to load file content'));
            }
            if (active) {
              const fileData = await fileResp.json();
              setFileError('');
              const content = fileData?.file?.content || '';
              setEncodedFileContent(content);
              const decodedContent = decodeBase64(content);
              setFileContent(decodedContent);
              setPreviewFilePath(pendingFile);
              setPreviewFileContent(decodedContent);
              setPreviewEncodedFileContent(content);
              setSelectedFileSize(getFilePayloadSize(fileData?.file, decodedContent));
              setError('');
            }
          } catch (err) {
            if (active && err?.name !== 'AbortError') {
              setFileContent('');
              setEncodedFileContent('');
              setSelectedFileSize(null);
              setFileError(err?.message || 'Unable to load file content.');
            }
          } finally {
            if (active) {
              setLoadingFilePath((path) => (path === pendingFile ? '' : path));
            }
          }
        } else {
          setTreeEntries({ '': rootEntries });
        }
      } catch (err) {
        if (!active || err?.name === 'AbortError') {
          return;
        }
        if (!pendingFileRef.current && !selectedFileRef.current) {
          setError('Unable to load entries. Confirm the file gateway is running and the slice exists.');
        }
      } finally {
        if (active) {
          setIsLoading(false);
        }
      }
    };

    loadRoot();

    return () => {
      active = false;
      controller.abort();
    };
  }, [
    buildEntriesUrl,
    buildFileUrl,
    canLoad,
    hasLoadedRootEntries,
    initialBrowserData,
    initialBrowserDataMatches,
    initialBrowserState,
    isActive,
    pendingFileRef,
    refreshHistoryToken,
    selectedFileRef,
    setEncodedFileContent,
    setError,
    setExpandedPaths,
    setFileContent,
    setFileError,
    setFocusedEntry,
    setIsLoading,
    setLoadingFilePath,
    setPreviewEncodedFileContent,
    setPreviewFileContent,
    setPreviewFilePath,
    setSelectedFile,
    setSelectedFileSize,
    setTreeEntries,
    sliceHash,
    sliceId,
  ]);

  const fetchEntries = useCallback(async (path) => {
    if (!canLoad) {
      return;
    }

    setIsLoading(true);
    setError('');

    try {
      const response = await fetchWithAuth(buildEntriesUrl(path));
      if (!response.ok) {
        throw new Error(`Request failed (${response.status})`);
      }
      const payload = await response.json();
      setTreeEntries((prev) => ({
        ...prev,
        [path]: payload.entries || [],
      }));
    } catch {
      if (!selectedFileRef.current) {
        setError('Unable to load entries. Confirm the file gateway is running and the slice exists.');
      }
    } finally {
      setIsLoading(false);
    }
  }, [buildEntriesUrl, canLoad, selectedFileRef, setError, setIsLoading, setTreeEntries]);

  useEffect(() => {
    if (!isActive || !canLoad || !selectedFile) {
      return undefined;
    }

    const parentPath = getParentDirectoryPath(selectedFile);
    if (!parentPath) {
      return undefined;
    }

    const missingAncestorPaths = getDirectoryAncestorPaths(parentPath).filter((path) => (
      !Object.prototype.hasOwnProperty.call(treeEntriesRef.current, path)
    ));
    if (missingAncestorPaths.length === 0) {
      return undefined;
    }

    let active = true;
    const controller = new AbortController();

    const hydrateFileAncestors = async () => {
      for (const path of missingAncestorPaths) {
        if (!active) {
          return;
        }
        if (Object.prototype.hasOwnProperty.call(treeEntriesRef.current, path)) {
          continue;
        }

        try {
          const response = await fetchWithAuth(buildEntriesUrl(path), {
            signal: controller.signal,
          });
          if (!response.ok) {
            return;
          }
          const payload = await response.json();
          if (!active) {
            return;
          }
          const entries = payload.entries || [];
          setTreeEntries((prev) => {
            if (Object.prototype.hasOwnProperty.call(prev, path)) {
              return prev;
            }
            return {
              ...prev,
              [path]: entries,
            };
          });
        } catch (err) {
          if (err?.name !== 'AbortError') {
            return;
          }
        }
      }
    };

    hydrateFileAncestors();

    return () => {
      active = false;
      controller.abort();
    };
  }, [buildEntriesUrl, canLoad, isActive, selectedFile, setTreeEntries, treeEntriesRef, treeEntriesScopeKey]);

  const openDirectoryPath = useCallback(async (targetPath, options = {}) => {
    if (!canLoad) {
      return;
    }
    const normalizedPath = normalizeWorkspaceResultPath(targetPath);
    const shouldToggleExpansion = Boolean(options.toggleExpansion);
    const isExpanded = expandedPaths.includes(normalizedPath);

    openFilesView();
    setFocusedEntry({ path: normalizedPath, type: 'directory' });
    clearFilePreview();
    setError('');
    if (options.updateHistory !== false) {
      writeBrowserState({ dir: normalizedPath });
    }

    if (!Object.prototype.hasOwnProperty.call(treeEntriesRef.current, normalizedPath)) {
      await fetchEntries(normalizedPath);
    }

    if (shouldToggleExpansion) {
      if (isExpanded && normalizedPath) {
        setExpandedPaths((prev) => prev.filter((path) => path !== normalizedPath));
        return;
      }
      setExpandedPaths((prev) => {
        const next = new Set(prev);
        getDirectoryAncestorPaths(normalizedPath).forEach((path) => next.add(path));
        return Array.from(next);
      });
      return;
    }

    setExpandedPaths((prev) => {
      const next = new Set(prev);
      getDirectoryAncestorPaths(normalizedPath).forEach((path) => next.add(path));
      return Array.from(next);
    });
  }, [
    canLoad,
    clearFilePreview,
    expandedPaths,
    fetchEntries,
    openFilesView,
    setError,
    setExpandedPaths,
    setFocusedEntry,
    treeEntriesRef,
    writeBrowserState,
  ]);

  useEffect(() => {
    if (!isActive || !canLoad || selectedFile || !selectedDirectoryPath) {
      return;
    }
    if (Object.prototype.hasOwnProperty.call(treeEntries, selectedDirectoryPath)) {
      return;
    }
    openDirectoryPath(selectedDirectoryPath, { updateHistory: false });
  }, [canLoad, isActive, openDirectoryPath, selectedDirectoryPath, selectedFile, treeEntries]);

  return { openDirectoryPath };
}
