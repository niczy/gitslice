import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';

import { fetchWithAuth } from '../../utils/api.js';
import { decodeBase64, highlightCodeLines } from '../../utils/highlight.js';
import { renderMarkdownHtml } from '../../utils/markdown.js';
import { normalizeEntryType } from '../../utils/normalize.js';
import { parseLocation } from '../../utils/routing.js';
import {
  normalizeWorkspaceResultPath,
  readBrowserErrorMessage,
} from './browserApi.js';
import {
  getDirectoryAncestorPaths,
  getFilePayloadSize,
  getNumericFileSize,
  getParentDirectoryPath,
  getPreviewMeta,
  getTreeFileSize,
} from './browserModel.js';
import { useRepoBrowserHistory } from './useRepoBrowserHistory.js';
import { useRepoFileDrafts } from './useRepoFileDrafts.js';

const useIsomorphicLayoutEffect = typeof window === 'undefined' ? useEffect : useLayoutEffect;

export function useRepoBrowserData({
  apiBaseUrl,
  buildEntriesUrl,
  buildFileUrl,
  buildRawFileUrl,
  buildRoutePath,
  canLoad,
  closeSidebar,
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
  refreshHistoryToken,
  setSliceHash,
  sliceHash,
  sliceId,
  treeEntriesScopeKey,
}) {
  const [treeEntries, setTreeEntries] = useState(() => (
    initialDataMatchesRawSlice && Array.isArray(initialBrowserData?.rootEntries)
      ? { '': initialBrowserData.rootEntries }
      : {}
  ));
  const [expandedPaths, setExpandedPaths] = useState(['']);
  const [selectedFile, setSelectedFile] = useState(() => initialSelectedFilePath || null);
  const [fileContent, setFileContent] = useState(() => (
    initialSelectedFilePayload?.content ? decodeBase64(initialSelectedFilePayload.content) : ''
  ));
  const [encodedFileContent, setEncodedFileContent] = useState(() => initialSelectedFilePayload?.content || '');
  const [previewFilePath, setPreviewFilePath] = useState(() => (
    initialSelectedFilePayload?.content ? initialSelectedFilePath : ''
  ));
  const [previewFileContent, setPreviewFileContent] = useState(() => (
    initialSelectedFilePayload?.content ? decodeBase64(initialSelectedFilePayload.content) : ''
  ));
  const [previewEncodedFileContent, setPreviewEncodedFileContent] = useState(() => initialSelectedFilePayload?.content || '');
  const [selectedFileSize, setSelectedFileSize] = useState(() => (
    getFilePayloadSize(
      initialSelectedFilePayload,
      initialSelectedFilePayload?.content ? decodeBase64(initialSelectedFilePayload.content) : '',
    )
  ));
  const [isLoading, setIsLoading] = useState(false);
  const [loadingFilePath, setLoadingFilePath] = useState(() => (
    initialSelectedFilePath && !hasInitialSelectedFilePayload ? initialSelectedFilePath : ''
  ));
  const [error, setError] = useState(() => (
    initialDataMatchesRawSlice ? initialBrowserData?.rootEntriesError || '' : ''
  ));
  const [fileError, setFileError] = useState(() => (
    initialDataMatchesRawSlice ? initialBrowserData?.selectedFileError || '' : ''
  ));
  const [focusedEntry, setFocusedEntry] = useState(() => (initialSelectedFilePath
    ? { path: initialSelectedFilePath, type: 'file' }
    : { path: initialSelectedDirectoryPath, type: 'directory' }));
  const {
    cancelFileEdit,
    confirmFileEdit,
    draftContent,
    fileDrafts,
    isEditingFile,
    resetAllDrafts,
    resetDraftState,
    setDraftContent,
    showFileEditor,
  } = useRepoFileDrafts({
    fileContent,
    initialDraftContent: initialSelectedFilePayload?.content ? decodeBase64(initialSelectedFilePayload.content) : '',
    selectedFile,
    setEncodedFileContent,
    setFileContent,
    setPreviewEncodedFileContent,
    setPreviewFileContent,
    setPreviewFilePath,
    setSelectedFileSize,
    setTreeEntries,
  });
  const {
    fileHistory,
    historyError,
    historyLoading,
    resetHistory,
    showHistory,
    toggleHistory,
  } = useRepoBrowserHistory({
    apiBaseUrl,
    isActive,
    refreshHistoryToken,
    selectedFile,
    sliceId,
  });

  const pendingFileRef = useRef(hasInitialSelectedFilePayload ? null : initialSelectedFilePath || null);
  const selectedFileRef = useRef(initialSelectedFilePath || null);
  const treeEntriesRef = useRef(treeEntries);
  const treeEntriesScopeRef = useRef('');
  const hasMountedSliceRef = useRef(false);
  const handledOpenFileRequestTokenRef = useRef(null);

  const highlightedContent = useMemo(() => highlightCodeLines(previewFileContent).html, [previewFileContent]);
  const markdownContent = useMemo(() => renderMarkdownHtml(previewFileContent), [previewFileContent]);
  const previewPath = previewFilePath || selectedFile || '';
  const previewMeta = useMemo(() => getPreviewMeta(previewPath, previewEncodedFileContent), [previewEncodedFileContent, previewPath]);
  const hasPreviewContent = Boolean(previewFilePath);
  const hasLoadedRootEntries = Object.prototype.hasOwnProperty.call(treeEntries, '');
  const isSelectedFileLoading = Boolean(selectedFile && loadingFilePath === selectedFile && !showHistory);
  const displayedFileSize = useMemo(() => {
    if (!selectedFile) {
      return null;
    }
    if (selectedFileSize !== null) {
      return selectedFileSize;
    }
    return isSelectedFileLoading ? null : fileContent.length;
  }, [fileContent.length, isSelectedFileLoading, selectedFile, selectedFileSize]);
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
    treeEntriesRef.current = treeEntries;
  }, [treeEntries]);

  const clearFilePreview = useCallback(() => {
    setSelectedFile(null);
    setFileContent('');
    setEncodedFileContent('');
    setPreviewFilePath('');
    setPreviewFileContent('');
    setPreviewEncodedFileContent('');
    setSelectedFileSize(null);
    resetDraftState('');
    setFileError('');
    setLoadingFilePath('');
    resetHistory();
  }, [resetDraftState, resetHistory]);

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

  const writeBrowserState = useCallback(({ file = '', dir = '' } = {}, options = {}) => {
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
    if (!isActive) {
      return;
    }
    if (!canLoad) {
      return;
    }
    if (initialBrowserDataMatches && Array.isArray(initialBrowserData?.rootEntries) && hasLoadedRootEntries) {
      return;
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

          for (let i = 1; i < parts.length; i++) {
            if (!active) return;
            const parentPath = parts.slice(0, i).join('/');
            pathsToExpand.push(parentPath);
            try {
              const dirResp = await fetchWithAuth(buildEntriesUrl(parentPath), { signal: controller.signal });
              if (dirResp.ok) {
                const dirData = await dirResp.json();
                allEntries[parentPath] = dirData.entries || [];
              }
            } catch (e) {
              if (e?.name === 'AbortError') return;
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
          } catch (e) {
            if (active && e?.name !== 'AbortError') {
              setFileContent('');
              setEncodedFileContent('');
              setSelectedFileSize(null);
              setFileError(e?.message || 'Unable to load file content.');
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
    refreshHistoryToken,
    sliceHash,
    sliceId,
  ]);

  useEffect(() => {
    if (!isActive || !canLoad || !selectedFile || isEditingFile) {
      return;
    }
    if (isLoading && loadingFilePath === selectedFile) {
      return;
    }
    if (
      initialBrowserDataMatches
      && initialBrowserData?.selectedFile === selectedFile
      && initialBrowserData?.selectedFilePayload?.content
      && !refreshHistoryToken
    ) {
      return;
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
  }, [buildEntriesUrl, canLoad]);

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
  }, [buildEntriesUrl, canLoad, isActive, selectedFile, treeEntriesScopeKey]);

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
  }, [canLoad, clearFilePreview, expandedPaths, fetchEntries, openFilesView, writeBrowserState]);

  useEffect(() => {
    if (!isActive || !canLoad || selectedFile || !selectedDirectoryPath) {
      return;
    }
    if (Object.prototype.hasOwnProperty.call(treeEntries, selectedDirectoryPath)) {
      return;
    }
    openDirectoryPath(selectedDirectoryPath, { updateHistory: false });
  }, [canLoad, isActive, openDirectoryPath, selectedDirectoryPath, selectedFile, treeEntries]);

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

  const openRawFile = useCallback(() => {
    if (!selectedFile || typeof window === 'undefined') {
      return;
    }
    window.open(buildRawFileUrl(selectedFile), '_blank', 'noopener,noreferrer');
  }, [buildRawFileUrl, selectedFile]);

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
    visibleEntryError,
  };
}
