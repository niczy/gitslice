import { useCallback, useEffect, useRef } from 'react';

import { fetchWithAuth } from '../../utils/api.js';
import { decodeBase64 } from '../../utils/highlight.js';
import {
  normalizeWorkspaceResultPath,
  readBrowserErrorMessage,
} from './browserApi.js';
import {
  getFilePayloadSize,
  getNumericFileSize,
  getTreeFileSize,
} from './browserModel.js';

export function useRepoBrowserFileLoader({
  buildEntriesUrl,
  buildFileUrl,
  canLoad,
  closeSidebar,
  expandedPaths,
  fileDrafts,
  initialBrowserData,
  initialBrowserDataMatches,
  isActive,
  isEditingFile,
  isLoading,
  loadingFilePath,
  openFileRequest,
  openFilesView,
  refreshHistoryToken,
  resetDraftState,
  resetHistory,
  selectedFile,
  selectedFileSize,
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
  writeBrowserState,
}) {
  const handledOpenFileRequestTokenRef = useRef(null);

  const openFilePath = useCallback(async (targetPath, options = {}) => {
    const normalizedPath = normalizeWorkspaceResultPath(targetPath);
    if (!canLoad || !normalizedPath) {
      return;
    }

    if (typeof window !== 'undefined' && window.innerWidth <= 900) {
      closeSidebar();
    }

    openFilesView();
    setFocusedEntry({ path: normalizedPath, type: 'file' });
    setSelectedFile(normalizedPath);
    setSelectedFileSize(
      getNumericFileSize(options.size)
        ?? getTreeFileSize(treeEntries, normalizedPath)
        ?? (selectedFile === normalizedPath ? selectedFileSize : null),
    );
    setFileContent('');
    setEncodedFileContent('');
    resetDraftState('');
    setIsLoading(true);
    setLoadingFilePath(normalizedPath);
    setError('');
    setFileError('');
    resetHistory();
    if (options.updateHistory !== false) {
      writeBrowserState({ file: normalizedPath });
    }

    const nextTreeEntries = { ...treeEntries };
    const nextExpandedPaths = new Set(['']);

    for (const path of expandedPaths) {
      nextExpandedPaths.add(path);
    }

    try {
      try {
        if (!nextTreeEntries['']) {
          const rootResponse = await fetchWithAuth(buildEntriesUrl(''));
          if (!rootResponse.ok) {
            throw new Error('Unable to load entries. Confirm the file gateway is running and the slice exists.');
          }
          const rootPayload = await rootResponse.json();
          nextTreeEntries[''] = rootPayload.entries || [];
        }

        const parts = normalizedPath.split('/');
        for (let i = 1; i < parts.length; i += 1) {
          const parentPath = parts.slice(0, i).join('/');
          nextExpandedPaths.add(parentPath);
          if (nextTreeEntries[parentPath]) {
            continue;
          }
          const dirResponse = await fetchWithAuth(buildEntriesUrl(parentPath));
          if (!dirResponse.ok) {
            throw new Error('Unable to load entries. Confirm the file gateway is running and the slice exists.');
          }
          const dirPayload = await dirResponse.json();
          nextTreeEntries[parentPath] = dirPayload.entries || [];
        }

        setTreeEntries(nextTreeEntries);
        setExpandedPaths(Array.from(nextExpandedPaths));
      } catch {
        setError('');
      }

      if (Object.prototype.hasOwnProperty.call(fileDrafts, normalizedPath)) {
        setFileContent(fileDrafts[normalizedPath]);
        setEncodedFileContent('');
        setPreviewFilePath(normalizedPath);
        setPreviewFileContent(fileDrafts[normalizedPath]);
        setPreviewEncodedFileContent('');
        setSelectedFileSize(fileDrafts[normalizedPath].length);
        resetDraftState(fileDrafts[normalizedPath]);
        setError('');
        return;
      }

      const fileResponse = await fetchWithAuth(buildFileUrl(normalizedPath));
      if (!fileResponse.ok) {
        throw new Error(await readBrowserErrorMessage(fileResponse, 'Unable to load file content'));
      }
      const filePayload = await fileResponse.json();
      const content = filePayload?.file?.content || '';
      setFileError('');
      setEncodedFileContent(content);
      const decodedContent = decodeBase64(content);
      setFileContent(decodedContent);
      setPreviewFilePath(normalizedPath);
      setPreviewFileContent(decodedContent);
      setPreviewEncodedFileContent(content);
      setSelectedFileSize(getFilePayloadSize(filePayload?.file, decodedContent));
      resetDraftState(decodedContent);
      setError('');
    } catch (err) {
      setFileContent('');
      setEncodedFileContent('');
      setSelectedFileSize(null);
      setFileError(err?.message || 'Unable to load file content.');
    } finally {
      setIsLoading(false);
      setLoadingFilePath((path) => (path === normalizedPath ? '' : path));
    }
  }, [
    buildEntriesUrl,
    buildFileUrl,
    canLoad,
    closeSidebar,
    expandedPaths,
    fileDrafts,
    openFilesView,
    resetDraftState,
    resetHistory,
    selectedFile,
    selectedFileSize,
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
    treeEntries,
    writeBrowserState,
  ]);

  useEffect(() => {
    if (!isActive || !openFileRequest?.path) {
      return;
    }
    if (handledOpenFileRequestTokenRef.current === openFileRequest.token) {
      return;
    }
    handledOpenFileRequestTokenRef.current = openFileRequest.token;
    openFilePath(openFileRequest.path);
  }, [isActive, openFilePath, openFileRequest]);

  useEffect(() => {
    if (!isActive || !canLoad || !selectedFile || isEditingFile) {
      return undefined;
    }
    if (isLoading && loadingFilePath === selectedFile) {
      return undefined;
    }
    if (
      initialBrowserDataMatches
      && initialBrowserData?.selectedFile === selectedFile
      && initialBrowserData?.selectedFilePayload?.content
      && !refreshHistoryToken
    ) {
      return undefined;
    }

    let active = true;
    const controller = new AbortController();

    const refreshSelectedFile = async () => {
      setLoadingFilePath(selectedFile);
      try {
        const response = await fetchWithAuth(buildFileUrl(selectedFile), {
          signal: controller.signal,
        });
        if (!response.ok) {
          throw new Error(await readBrowserErrorMessage(response, 'Unable to load file content'));
        }
        const payload = await response.json();
        if (!active) {
          return;
        }
        const content = payload?.file?.content || '';
        const decodedContent = decodeBase64(content);
        setFileError('');
        setEncodedFileContent(content);
        setFileContent(decodedContent);
        setPreviewFilePath(selectedFile);
        setPreviewFileContent(decodedContent);
        setPreviewEncodedFileContent(content);
        setSelectedFileSize(getFilePayloadSize(payload?.file, decodedContent));
        resetDraftState(decodedContent);
        setError('');
      } catch (err) {
        if (!active || err?.name === 'AbortError') {
          return;
        }
        setFileError(err?.message || 'Unable to load file content.');
      } finally {
        if (active) {
          setLoadingFilePath((path) => (path === selectedFile ? '' : path));
        }
      }
    };

    refreshSelectedFile();

    return () => {
      active = false;
      controller.abort();
    };
  }, [
    buildFileUrl,
    canLoad,
    initialBrowserData,
    initialBrowserDataMatches,
    isActive,
    isEditingFile,
    isLoading,
    loadingFilePath,
    refreshHistoryToken,
    resetDraftState,
    selectedFile,
    setEncodedFileContent,
    setError,
    setFileContent,
    setFileError,
    setLoadingFilePath,
    setPreviewEncodedFileContent,
    setPreviewFileContent,
    setPreviewFilePath,
    setSelectedFileSize,
    sliceHash,
    sliceId,
  ]);

  return { openFilePath };
}
