import { useCallback, useEffect, useLayoutEffect, useMemo, useRef } from 'react';

import { decodeBase64 } from '../../utils/highlight.js';
import { normalizeEntryType } from '../../utils/normalize.js';
import { normalizeWorkspaceResultPath } from './browserApi.js';
import {
  getFilePayloadSize,
  getParentDirectoryPath,
  getTreeFileSize,
} from './browserModel.js';
import { useRepoBrowserFileState } from './useRepoBrowserFileState.js';
import { useRepoBrowserFileLoader } from './useRepoBrowserFileLoader.js';
import {
  useRepoBrowserRouteReader,
  useRepoBrowserRouteWriter,
} from './useRepoBrowserRouteState.js';
import { useRepoBrowserTreeLoader } from './useRepoBrowserTreeLoader.js';
import { useRepoBrowserTreeActions } from './useRepoBrowserTreeActions.js';
import { useRepoBrowserTreeState } from './useRepoBrowserTreeState.js';

const useIsomorphicLayoutEffect = typeof window === 'undefined' ? useEffect : useLayoutEffect;

export function useRepoBrowserData({
  apiBaseUrl,
  buildEntriesUrl,
  buildFileUrl,
  buildRawFileUrl,
  buildRoutePath,
  canLoad,
  closeSidebar,
  currentSlice,
  currentSliceDisplayName,
  currentSliceLabel,
  hasInitialSelectedFilePayload,
  initialBrowserData,
  initialBrowserDataMatches,
  initialBrowserState,
  initialDataMatchesRawSlice,
  initialSelectedDirectoryPath,
  initialSelectedFilePath,
  initialSelectedFilePayload,
  isActive,
  openFileRequest,
  openFilesView,
  openSidebar,
  refreshHistoryToken,
  setSliceHash,
  sliceHash,
  sliceId,
  treeEntriesScopeKey,
}) {
  const {
    error,
    expandedPaths,
    focusedEntry,
    hasLoadedRootEntries,
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
  } = useRepoBrowserTreeState({
    initialBrowserData,
    initialDataMatchesRawSlice,
    initialSelectedDirectoryPath,
    initialSelectedFilePath,
  });

  const {
    cancelFileEdit,
    clearFilePreview,
    confirmFileEdit,
    displayedFileSize,
    draftContent,
    fileDrafts,
    fileError,
    fileHistory,
    hasPreviewContent,
    highlightedContent,
    historyError,
    historyLoading,
    isEditingFile,
    isSelectedFileLoading,
    loadingFilePath,
    markdownContent,
    openRawFile,
    previewMeta,
    previewPath,
    resetAllDrafts,
    resetDraftState,
    resetHistory,
    selectedFile,
    selectedFileSize,
    setDraftContent,
    setEncodedFileContent,
    setFileContent,
    setFileError,
    setLoadingFilePath,
    setPreviewEncodedFileContent,
    setPreviewFileContent,
    setPreviewFilePath,
    setSelectedFile,
    setSelectedFileSize,
    showFileEditor,
    showHistory,
    toggleHistory,
  } = useRepoBrowserFileState({
    apiBaseUrl,
    buildRawFileUrl,
    hasInitialSelectedFilePayload,
    initialBrowserData,
    initialDataMatchesRawSlice,
    initialSelectedFilePath,
    initialSelectedFilePayload,
    isActive,
    refreshHistoryToken,
    setTreeEntries,
    sliceId,
  });

  const pendingFileRef = useRef(hasInitialSelectedFilePayload ? null : initialSelectedFilePath || null);
  const selectedFileRef = useRef(initialSelectedFilePath || null);
  const skipNextSliceHashResetRef = useRef(false);

  const visibleEntryError = selectedFile ? '' : error;
  const selectedDirectoryPath = useMemo(() => {
    if (selectedFile || focusedEntry?.type !== 'directory') {
      return '';
    }
    return normalizeWorkspaceResultPath(focusedEntry.path);
  }, [focusedEntry, selectedFile]);
  const activeBrowserPath = selectedFile || selectedDirectoryPath;

  useEffect(() => {
    selectedFileRef.current = selectedFile;
  }, [selectedFile]);

  useIsomorphicLayoutEffect(() => {
    if (!initialBrowserDataMatches) {
      return;
    }
    const nextSelectedFile = initialBrowserData?.selectedFile || initialBrowserState?.file || '';
    const nextFilePayload = initialBrowserData?.selectedFilePayload || null;

    if (Array.isArray(initialBrowserData?.rootEntries)) {
      const shouldPreserveTree = treeEntriesScopeRef.current === treeEntriesScopeKey;
      treeEntriesScopeRef.current = treeEntriesScopeKey;
      setTreeEntries((prev) => (
        shouldPreserveTree
          ? { ...prev, '': initialBrowserData.rootEntries }
          : { '': initialBrowserData.rootEntries }
      ));
      setExpandedPaths((prev) => (
        shouldPreserveTree && prev.includes('') ? prev : ['']
      ));
      setError(nextSelectedFile ? '' : initialBrowserData.rootEntriesError || '');
    } else if (initialBrowserData?.rootEntriesError) {
      setError(nextSelectedFile ? '' : initialBrowserData.rootEntriesError);
    }

    if (nextSelectedFile && nextFilePayload?.content) {
      const decodedContent = decodeBase64(nextFilePayload.content);
      pendingFileRef.current = null;
      setSelectedFile(nextSelectedFile);
      setFocusedEntry({ path: nextSelectedFile, type: 'file' });
      setEncodedFileContent(nextFilePayload.content);
      setFileContent(decodedContent);
      setPreviewFilePath(nextSelectedFile);
      setPreviewFileContent(decodedContent);
      setPreviewEncodedFileContent(nextFilePayload.content);
      setSelectedFileSize(getFilePayloadSize(nextFilePayload, decodedContent));
      setDraftContent(decodedContent);
      setFileError(initialBrowserData.selectedFileError || '');
      setLoadingFilePath('');
      setError('');
      return;
    }

    if (nextSelectedFile) {
      pendingFileRef.current = nextSelectedFile;
      setSelectedFile(nextSelectedFile);
      setFocusedEntry({ path: nextSelectedFile, type: 'file' });
      setFileContent('');
      setEncodedFileContent('');
      setPreviewFilePath('');
      setPreviewFileContent('');
      setPreviewEncodedFileContent('');
      setSelectedFileSize(getTreeFileSize(
        Array.isArray(initialBrowserData?.rootEntries) ? { '': initialBrowserData.rootEntries } : {},
        nextSelectedFile,
      ));
      setDraftContent('');
      setFileError(initialBrowserData?.selectedFileError || '');
      setLoadingFilePath(nextSelectedFile);
      setError('');
      return;
    }

    pendingFileRef.current = null;
    setSelectedFile(null);
    setFileContent('');
    setEncodedFileContent('');
    setPreviewFilePath('');
    setPreviewFileContent('');
    setPreviewEncodedFileContent('');
    setSelectedFileSize(null);
    setDraftContent('');
    setFileError('');
    setLoadingFilePath('');
  }, [initialBrowserData, initialBrowserDataMatches, initialBrowserState, setDraftContent, treeEntriesScopeKey]);

  useEffect(() => {
    if (!hasMountedSliceRef.current) {
      hasMountedSliceRef.current = true;
      return;
    }
    if (skipNextSliceHashResetRef.current) {
      skipNextSliceHashResetRef.current = false;
      treeEntriesScopeRef.current = treeEntriesScopeKey;
      return;
    }
    if (initialBrowserDataMatches && Array.isArray(initialBrowserData?.rootEntries)) {
      return;
    }
    treeEntriesScopeRef.current = treeEntriesScopeKey;
    setTreeEntries({});
    setExpandedPaths(['']);
    pendingFileRef.current = null;
    setSelectedFile(null);
    setFileContent('');
    setEncodedFileContent('');
    setPreviewFilePath('');
    setPreviewFileContent('');
    setPreviewEncodedFileContent('');
    setSelectedFileSize(null);
    resetAllDrafts();
    setFileError('');
    setLoadingFilePath('');
    setFocusedEntry({ path: '', type: 'directory' });
  }, [initialBrowserData, initialBrowserDataMatches, resetAllDrafts, sliceHash, sliceId, treeEntriesScopeKey]);

  const setResolvedSliceHash = useCallback((nextSliceHash) => {
    skipNextSliceHashResetRef.current = true;
    setSliceHash(nextSliceHash);
  }, [setSliceHash]);

  const writeBrowserState = useRepoBrowserRouteWriter({
    buildRoutePath,
    sliceHash,
    sliceId,
  });
  const canPinEntriesSliceHash = !((currentSlice?.folder_mounts || currentSlice?.folderMounts || []).length > 0);

  const { openFilePath } = useRepoBrowserFileLoader({
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
  });

  const { openDirectoryPath } = useRepoBrowserTreeLoader({
    buildEntriesUrl,
    buildFileUrl,
    canPinEntriesSliceHash,
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
    setSliceHash: setResolvedSliceHash,
    setTreeEntries,
    sliceHash,
    sliceId,
    treeEntries,
    treeEntriesRef,
    treeEntriesScopeKey,
    writeBrowserState,
  });

  const { getCreateTreeEntryBlockedReason, handleTreeAction, treeActionState } = useRepoBrowserTreeActions({
    apiBaseUrl,
    buildEntriesUrl,
    buildFileUrl,
    canLoad,
    clearFilePreview,
    currentSlice,
    focusedEntry,
    openFilesView,
    openSidebar,
    setError,
    setExpandedPaths,
    setFileError,
    setFocusedEntry,
    setIsLoading,
    setSliceHash,
    setTreeEntries,
    sliceHash,
    sliceId,
    treeEntriesScopeRef,
    writeBrowserState,
  });

  useRepoBrowserRouteReader({
    isActive,
    openDirectoryPath,
    openFilePath,
    setSliceHash,
    sliceId,
  });

  const handleEntryClick = useCallback(async (entry) => {
    const entryKind = normalizeEntryType(entry.type);
    if (entryKind === 'directory') {
      await openDirectoryPath(entry.path, { toggleExpansion: true });
      return;
    }
    setFocusedEntry({ path: entry.path, type: entryKind });
    await openFilePath(entry.path, { size: entry.size });
  }, [openDirectoryPath, openFilePath]);

  const handleBreadcrumbClick = useCallback(async (path) => {
    const normalizedPath = normalizeWorkspaceResultPath(path);
    const normalizedSelectedFile = normalizeWorkspaceResultPath(selectedFile);
    const directoryPath = normalizedSelectedFile && normalizedPath === normalizedSelectedFile
      ? getParentDirectoryPath(normalizedSelectedFile)
      : normalizedPath;
    await openDirectoryPath(directoryPath);
  }, [openDirectoryPath, selectedFile]);

  const selectedDirectoryEntries = useMemo(() => (
    treeEntries[selectedDirectoryPath] || []
  ), [selectedDirectoryPath, treeEntries]);
  const hasSelectedDirectoryEntries = Object.prototype.hasOwnProperty.call(treeEntries, selectedDirectoryPath);
  const selectedDirectoryLabel = selectedDirectoryPath
    ? selectedDirectoryPath.split('/').filter(Boolean).pop()
    : currentSliceDisplayName || currentSliceLabel || 'Files';

  const handleContentEntryClick = useCallback(async (entry) => {
    const entryKind = normalizeEntryType(entry.type);
    if (entryKind === 'directory') {
      await openDirectoryPath(entry.path);
      return;
    }
    setFocusedEntry({ path: entry.path, type: 'file' });
    await openFilePath(entry.path, { size: entry.size });
  }, [openDirectoryPath, openFilePath]);

  return {
    activeBrowserPath,
    cancelFileEdit,
    confirmFileEdit,
    displayedFileSize,
    draftContent,
    expandedPaths,
    fileError,
    fileHistory,
    focusedEntry,
    handleBreadcrumbClick,
    handleContentEntryClick,
    handleEntryClick,
    getCreateTreeEntryBlockedReason,
    handleTreeAction,
    hasLoadedRootEntries,
    hasPreviewContent,
    hasSelectedDirectoryEntries,
    highlightedContent,
    historyError,
    historyLoading,
    isEditingFile,
    isLoading,
    isSelectedFileLoading,
    markdownContent,
    openRawFile,
    previewMeta,
    previewPath,
    selectedDirectoryEntries,
    selectedDirectoryLabel,
    selectedDirectoryPath,
    selectedFile,
    setDraftContent,
    showFileEditor,
    showHistory,
    toggleHistory,
    treeEntries,
    treeActionState,
    visibleEntryError,
  };
}
