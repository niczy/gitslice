import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import {
  Check,
  ChevronDown,
  ChevronRight,
  Edit3,
  ExternalLink,
  FileText,
  Folder,
  FolderOpen,
  History,
  Menu,
  PanelLeftClose,
  PanelLeftOpen,
  Settings,
  X,
} from 'lucide-react';
import { apiBaseUrl, fetchWithAuth } from '../utils/api.js';
import { formatBytes } from '../utils/format.js';
import { formatChangeType, formatTimestamp } from '../utils/format.js';
import { normalizeChange, normalizeChangeType, normalizeEntryType } from '../utils/normalize.js';
import { decodeBase64, highlightCodeLines } from '../utils/highlight.js';
import { renderMarkdownHtml } from '../utils/markdown.js';
import { getSliceDisplayName } from '../utils/slices.js';
import { buildBrowserPath, parseLocation } from '../utils/routing.js';
import {
  SIDEBAR_WIDTH_MAX,
  SIDEBAR_WIDTH_MIN,
} from '../features/browser/browserConstants.js';
import {
  getDirectoryAncestorPaths,
  getEntryDisplayPath,
  getEntryName,
  getFilePayloadSize,
  getNumericFileSize,
  getParentDirectoryPath,
  getPreviewMeta,
  getTreeFileSize,
  sortEntriesByTypeAndName,
} from '../features/browser/browserModel.js';
import { useBrowserSidebar } from '../features/browser/useBrowserSidebar.js';
import SliceDetailNav from './SliceDetailNav.jsx';
import SliceSettings from './SliceSettings.jsx';
import { Button } from './ui/button.jsx';

const useIsomorphicLayoutEffect = typeof window === 'undefined' ? useEffect : useLayoutEffect;

// ---------------------------------------------------------------------------
// Repo Browser Component
// ---------------------------------------------------------------------------

export default function RepoBrowser({
  slices,
  currentSliceId,
  authUsername,
  publicApiBaseUrl = '',
  onSliceChange,
  onNavigateToDiff,
  onOpenCommits,
  onOpenChangesets,
  onOpenAgents,
  refreshHistoryToken,
  isActive,
  slicesLoading,
  openFileRequest,
  initialBrowserData,
}) {
  // Parse initial browser state from the current route on mount.
  const initialBrowserState = useMemo(() => {
    if (typeof window === 'undefined') {
      return null;
    }
    const route = parseLocation(window.location);
    return route.page === 'browser' ? route.browserState || null : null;
  }, []);

  const initialDataMatchesRawSlice = initialBrowserData?.selectedSliceId === currentSliceId
    && String(initialBrowserData?.sliceHash || '') === String(initialBrowserState?.sliceHash || '');
  const initialSelectedFilePayload = initialDataMatchesRawSlice ? initialBrowserData?.selectedFilePayload : null;
  const initialSelectedFilePath = initialDataMatchesRawSlice
    ? initialBrowserData?.selectedFile || initialBrowserState?.file || ''
    : initialBrowserState?.file || '';
  const initialSelectedDirectoryPath = initialSelectedFilePath
    ? ''
    : String(initialBrowserState?.dir || '').replace(/^\/+/, '');
  const hasInitialSelectedFilePayload = Boolean(initialSelectedFilePayload?.content);
  const [sliceHash, setSliceHash] = useState(initialBrowserState?.sliceHash || '');
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
  const [draftContent, setDraftContent] = useState(() => (
    initialSelectedFilePayload?.content ? decodeBase64(initialSelectedFilePayload.content) : ''
  ));
  const [fileDrafts, setFileDrafts] = useState({});
  const [isEditingFile, setIsEditingFile] = useState(false);
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
  const [showHistory, setShowHistory] = useState(false);
  const [fileHistory, setFileHistory] = useState([]);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [historyError, setHistoryError] = useState('');
  const [isSettingsOpen, setIsSettingsOpen] = useState(false);
  const [isActionMenuOpen, setIsActionMenuOpen] = useState(false);
  const {
    closeSidebar,
    handleSidebarResizeKeyDown,
    isCompactHeader,
    isResizingSidebar,
    isSidebarDismissing,
    openSidebar,
    sidebarOpen,
    sidebarWidth,
    startSidebarResize,
  } = useBrowserSidebar();

  // File to restore after root tree entries load (from URL hash)
  const pendingFileRef = useRef(hasInitialSelectedFilePayload ? null : initialSelectedFilePath || null);
  const selectedFileRef = useRef(initialSelectedFilePath || null);
  const treeEntriesRef = useRef(treeEntries);
  const treeEntriesScopeRef = useRef('');
  const hasAppliedInitialSliceRef = useRef(false);
  const hasMountedSliceRef = useRef(false);
  const actionMenuRef = useRef(null);
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

  useEffect(() => {
    selectedFileRef.current = selectedFile;
  }, [selectedFile]);

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

  useIsomorphicLayoutEffect(() => {
    treeEntriesRef.current = treeEntries;
  }, [treeEntries]);

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
  const viewingSettings = isSettingsOpen && canShowSettings;

  const openFilesView = useCallback(() => {
    setIsSettingsOpen(false);
  }, []);

  const openSettingsView = useCallback(() => {
    setIsSettingsOpen(true);
  }, []);

  const normalizeWorkspaceResultPath = useCallback((value) => String(value || '').replace(/^\/+/, ''), []);

  const clearFilePreview = useCallback(() => {
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
    setShowHistory(false);
    setFileHistory([]);
    setHistoryError('');
    setIsEditingFile(false);
  }, []);

  useEffect(() => {
    if (!canShowSettings && isSettingsOpen) {
      setIsSettingsOpen(false);
    }
  }, [canShowSettings, isSettingsOpen]);

  useEffect(() => {
    setIsSettingsOpen(false);
  }, [sliceId, sliceHash]);

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
  }, [initialBrowserData, initialBrowserDataMatches, treeEntriesScopeKey]);

  useEffect(() => {
    if (!viewingSettings || typeof window === 'undefined') {
      return undefined;
    }
    const handleKeyDown = (event) => {
      if (event.key === 'Escape') {
        setIsSettingsOpen(false);
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [viewingSettings]);

  const selectedDirectoryPath = useMemo(() => {
    if (selectedFile || focusedEntry?.type !== 'directory') {
      return '';
    }
    return normalizeWorkspaceResultPath(focusedEntry.path);
  }, [focusedEntry, normalizeWorkspaceResultPath, selectedFile]);

  const activeBrowserPath = selectedFile || selectedDirectoryPath;

  const breadcrumbs = useMemo(() => {
    const slicePrefix = currentSliceLabel || 'slice';
    if (!activeBrowserPath) {
      return [{ name: slicePrefix, path: '' }];
    }
    const parts = activeBrowserPath.split('/');
    return [
      { name: slicePrefix, path: '' },
      ...parts.map((part, index) => ({
        name: part,
        path: parts.slice(0, index + 1).join('/'),
      })),
    ];
  }, [activeBrowserPath, currentSliceLabel]);

  const visibleBreadcrumbs = useMemo(() => {
    const maxBreadcrumbs = isCompactHeader ? 4 : 8;
    if (breadcrumbs.length <= maxBreadcrumbs) {
      return breadcrumbs;
    }

    const trailingCount = Math.max(maxBreadcrumbs - 2, 2);
    const ellipsisTarget = breadcrumbs[breadcrumbs.length - trailingCount - 1];
    return [
      breadcrumbs[0],
      { name: '…', path: ellipsisTarget?.path || '' },
      ...breadcrumbs.slice(-trailingCount),
    ];
  }, [breadcrumbs, isCompactHeader]);

  useEffect(() => {
    if (!isCompactHeader) {
      setIsActionMenuOpen(false);
    }
  }, [isCompactHeader]);

  useEffect(() => {
    if (!isActionMenuOpen) {
      return undefined;
    }
    if (typeof document === 'undefined') {
      return undefined;
    }

    const handleClickOutside = (event) => {
      if (actionMenuRef.current && !actionMenuRef.current.contains(event.target)) {
        setIsActionMenuOpen(false);
      }
    };

    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, [isActionMenuOpen]);

  // Update slice from URL if present
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
  }, [initialBrowserState, resolveRequestedSliceId, currentSliceId, onSliceChange, slicesLoading]);

  // Reset tree when slice changes
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
    setDraftContent('');
    setFileDrafts({});
    setIsEditingFile(false);
    setFileError('');
    setLoadingFilePath('');
    setFocusedEntry({ path: '', type: 'directory' });
  }, [initialBrowserData, initialBrowserDataMatches, sliceId, sliceHash, treeEntriesScopeKey]);

  const encodePath = useCallback((value) => value.split('/').map(encodeURIComponent).join('/'), []);

  // Build URL for entries endpoint based on mode
  const buildEntriesUrl = useCallback((path) => {
    const params = new URLSearchParams();
    const encodedPath = path ? encodePath(path) : '';
    const pathSuffix = encodedPath ? `/${encodedPath}` : '';

    if (sliceHash) {
      params.set('slice_version.slice_hash', sliceHash);
    }
    const queryString = params.toString();
    return `${apiBaseUrl}/v1/slices/${sliceId}/entries${pathSuffix}${queryString ? `?${queryString}` : ''}`;
  }, [encodePath, sliceHash, sliceId]);

  // Build URL for file endpoint based on mode
  const buildFileUrl = useCallback((filePath) => {
    const encodedPath = filePath ? encodePath(filePath) : '';
    const pathSuffix = encodedPath ? `/${encodedPath}` : '';
    const params = new URLSearchParams();

    if (sliceHash) {
      params.set('slice_version.slice_hash', sliceHash);
    }
    const queryString = params.toString();
    return `${apiBaseUrl}/v1/slices/${sliceId}/files${pathSuffix}${queryString ? `?${queryString}` : ''}`;
  }, [encodePath, sliceHash, sliceId]);

  const buildRawFileUrl = useCallback((filePath) => {
    const encodedPath = filePath ? encodePath(filePath) : '';
    const params = new URLSearchParams();
    if (sliceHash) {
      params.set('slice_version.slice_hash', sliceHash);
    }
    const queryString = params.toString();
    return `/raw/slices/${encodeURIComponent(sliceId)}/${encodedPath}${queryString ? `?${queryString}` : ''}`;
  }, [encodePath, sliceHash, sliceId]);

  // Build URL for file history endpoint based on mode
  const buildHistoryUrl = useCallback((filePath) => {
    const encodedPath = filePath ? encodePath(filePath) : '';
    const pathSuffix = encodedPath ? `/${encodedPath}` : '';
    return `${apiBaseUrl}/v1/slices/${sliceId}/files/history${pathSuffix}`;
  }, [encodePath, sliceId]);

  // Fetch file history from the API
  const readErrorMessage = async (response, fallbackMessage) => {
    let detail = '';
    try {
      const text = await response.text();
      if (text) {
        try {
          const payload = JSON.parse(text);
          detail = payload?.message || payload?.error || '';
        } catch {
          detail = text;
        }
      }
    } catch {
      detail = '';
    }
    if (!detail) {
      return `${fallbackMessage} (${response.status})`;
    }
    return `${fallbackMessage}: ${detail}`;
  };

  // Fetch file history from the API
  const fetchFileHistory = useCallback(async (filePath) => {
    if (!filePath) {
      return;
    }

    setHistoryLoading(true);
    setHistoryError('');

    try {
      const response = await fetchWithAuth(buildHistoryUrl(filePath));
      if (!response.ok) {
        throw new Error(`Request failed (${response.status})`);
      }
      const payload = await response.json();
      setFileHistory((payload.changes || []).map(normalizeChange));
    } catch (err) {
      setHistoryError('Unable to load file history.');
      setFileHistory([]);
    } finally {
      setHistoryLoading(false);
    }
  }, [buildHistoryUrl]);

  // Toggle history panel
  const toggleHistory = () => {
    const newShowHistory = !showHistory;
    setShowHistory(newShowHistory);
    if (newShowHistory && selectedFile && fileHistory.length === 0) {
      fetchFileHistory(selectedFile);
    }
  };

  const writeBrowserState = useCallback(({ file = '', dir = '' } = {}, options = {}) => {
    if (typeof window === 'undefined') {
      return;
    }
    const currentRoute = parseLocation(window.location);
    if (currentRoute.page !== 'browser' || !currentRoute.browserState?.slice) {
      return;
    }

    const nextPath = buildBrowserPath({
      dir,
      file,
      slice: sliceId,
      sliceHash,
    });
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
  }, [sliceHash, sliceId]);

  const openFilePath = useCallback(async (targetPath, options = {}) => {
    const normalizedPath = normalizeWorkspaceResultPath(targetPath);
    if (!canLoad || !normalizedPath) {
      return;
    }

    if (typeof window !== 'undefined' && window.innerWidth <= 900) {
      closeSidebar();
    }

    setIsSettingsOpen(false);
    setFocusedEntry({ path: normalizedPath, type: 'file' });
    setSelectedFile(normalizedPath);
    setSelectedFileSize(
      getNumericFileSize(options.size)
        ?? getTreeFileSize(treeEntries, normalizedPath)
        ?? (selectedFile === normalizedPath ? selectedFileSize : null),
    );
    setFileContent('');
    setEncodedFileContent('');
    setDraftContent('');
    setIsLoading(true);
    setLoadingFilePath(normalizedPath);
    setError('');
    setFileError('');
    setShowHistory(false);
    setFileHistory([]);
    setHistoryError('');
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
        setDraftContent(fileDrafts[normalizedPath]);
        setIsEditingFile(false);
        setError('');
        return;
      }

      const fileResponse = await fetchWithAuth(buildFileUrl(normalizedPath));
      if (!fileResponse.ok) {
        throw new Error(await readErrorMessage(fileResponse, 'Unable to load file content'));
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
      setDraftContent(decodedContent);
      setIsEditingFile(false);
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
    normalizeWorkspaceResultPath,
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

  const renderFileActions = (onActionDone) => {
    if (!selectedFile) {
      return null;
    }

    return (
      <>
        {!showHistory && (
          <>
            <Button
              type="button"
              variant="secondary"
              className={`history-toggle ${isEditingFile ? 'active' : ''}`}
              onClick={() => {
                if (isEditingFile) {
                  setDraftContent(fileContent);
                  setIsEditingFile(false);
                } else {
                  setDraftContent(fileContent);
                  setIsEditingFile(true);
                }
                onActionDone?.();
              }}
              >
                {isEditingFile ? <X size={15} aria-hidden="true" /> : <Edit3 size={15} aria-hidden="true" />}
                {isEditingFile ? 'Cancel' : 'Edit'}
              </Button>
            {isEditingFile && (
              <Button
                type="button"
                variant="default"
                className="history-toggle browser-commit-button"
                onClick={() => {
                  confirmFileEdit();
                  onActionDone?.();
                }}
              >
                <Check size={15} aria-hidden="true" />
                Commit Changes
              </Button>
            )}
          </>
        )}
        <Button
          type="button"
          variant="secondary"
          className={`history-toggle ${showHistory ? 'active' : ''}`}
          onClick={() => {
            toggleHistory();
            onActionDone?.();
          }}
          data-testid="history-toggle"
          title={showHistory ? 'Show file content' : 'Show commit history'}
        >
          {showHistory ? <FileText size={15} aria-hidden="true" /> : <History size={15} aria-hidden="true" />}
          {showHistory ? 'Content' : 'History'}
        </Button>
        {!showHistory && !isEditingFile && (
          <Button
            type="button"
            variant="secondary"
            className="history-toggle"
            onClick={() => {
              window.open(buildRawFileUrl(selectedFile), '_blank', 'noopener,noreferrer');
              onActionDone?.();
            }}
            title="Open raw file"
          >
            <ExternalLink size={15} aria-hidden="true" />
            Raw
          </Button>
        )}
      </>
    );
  };

  useEffect(() => {
    if (!isActive || !showHistory || !selectedFile || !refreshHistoryToken) {
      return;
    }
    fetchFileHistory(selectedFile);
  }, [fetchFileHistory, isActive, refreshHistoryToken, selectedFile, showHistory]);

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

        // Restore file selection from URL hash if pending.
        // If the URL also requested a specific slice, wait to restore the file
        // until that slice is active; otherwise refresh can load the wrong slice
        // first, consume the pending file, and then drop the file selection.
        const pendingFile = pendingFileRef.current;
        const shouldRestorePendingFile = !initialBrowserState?.slice || initialBrowserState.slice === sliceId;
        if (pendingFile && shouldRestorePendingFile) {
          pendingFileRef.current = null;

          const parts = pendingFile.split('/');
          const allEntries = { '': rootEntries };
          const pathsToExpand = [''];

          // Load parent directory entries along the file path
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

          // Load file content
          try {
            const fileResp = await fetchWithAuth(buildFileUrl(pendingFile), { signal: controller.signal });
            if (!fileResp.ok) {
              throw new Error(await readErrorMessage(fileResp, 'Unable to load file content'));
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
          throw new Error(await readErrorMessage(response, 'Unable to load file content'));
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
        setDraftContent(decodedContent);
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
    selectedFile,
    sliceHash,
    sliceId,
  ]);

  const fetchEntries = async (path) => {
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
    } catch (err) {
      if (!selectedFileRef.current) {
        setError('Unable to load entries. Confirm the file gateway is running and the slice exists.');
      }
    } finally {
      setIsLoading(false);
    }
  };

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

  const openDirectoryPath = async (targetPath, options = {}) => {
    if (!canLoad) {
      return;
    }
    const normalizedPath = normalizeWorkspaceResultPath(targetPath);
    const shouldToggleExpansion = Boolean(options.toggleExpansion);
    const isExpanded = expandedPaths.includes(normalizedPath);

    setIsSettingsOpen(false);
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
  };

  useEffect(() => {
    if (!isActive || !canLoad || selectedFile || !selectedDirectoryPath) {
      return;
    }
    if (Object.prototype.hasOwnProperty.call(treeEntries, selectedDirectoryPath)) {
      return;
    }
    openDirectoryPath(selectedDirectoryPath, { updateHistory: false });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [canLoad, isActive, selectedDirectoryPath, selectedFile, treeEntries]);

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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isActive, normalizeWorkspaceResultPath, openFilePath, sliceId]);

  const handleEntryClick = async (entry) => {
    const entryKind = normalizeEntryType(entry.type);
    if (entryKind === 'directory') {
      await openDirectoryPath(entry.path, { toggleExpansion: true });
      return;
    }
    setFocusedEntry({ path: entry.path, type: entryKind });
    await openFilePath(entry.path, { size: entry.size });
  };

  const confirmFileEdit = () => {
    if (!selectedFile) {
      return;
    }
    setFileDrafts((prev) => ({ ...prev, [selectedFile]: draftContent }));
    setFileContent(draftContent);
    setEncodedFileContent('');
    setPreviewFilePath(selectedFile);
    setPreviewFileContent(draftContent);
    setPreviewEncodedFileContent('');
    setSelectedFileSize(draftContent.length);
    setIsEditingFile(false);
    const parentPath = selectedFile.includes('/') ? selectedFile.split('/').slice(0, -1).join('/') : '';
    setTreeEntries((prev) => {
      const entries = prev[parentPath] || [];
      const nextEntries = entries.map((entry) => {
        if (entry.path !== selectedFile) {
          return entry;
        }
        return { ...entry, size: draftContent.length };
      });
      return { ...prev, [parentPath]: nextEntries };
    });
  };

  const handleBreadcrumbClick = async (path) => {
    const normalizedPath = normalizeWorkspaceResultPath(path);
    const normalizedSelectedFile = normalizeWorkspaceResultPath(selectedFile);
    const directoryPath = normalizedSelectedFile && normalizedPath === normalizedSelectedFile
      ? getParentDirectoryPath(normalizedSelectedFile)
      : normalizedPath;
    await openDirectoryPath(directoryPath);
  };

  const renderTree = (path, depth = 0) => {
    const entries = sortEntriesByTypeAndName(treeEntries[path] || []);
    return (
      <ul className="tree-list">
        {entries.map((entry) => {
          const entryKind = normalizeEntryType(entry.type);
          const isExpanded = expandedPaths.includes(entry.path);
          const entryLabel = getEntryName(entry);
          return (
            <li key={entry.path}>
              <Button
                type="button"
                variant="ghost"
                className={`tree-entry ${entryKind}${focusedEntry?.path === entry.path ? ' active' : ''}`}
                style={{ paddingLeft: `${depth * 14 + 8}px` }}
                title={getEntryDisplayPath(entry)}
                onClick={() => handleEntryClick(entry)}
              >
                <span className="tree-caret" aria-hidden="true">
                  {entryKind === 'directory'
                    ? (isExpanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />)
                    : <span className="tree-caret-dot" />}
                </span>
                <span className="entry-icon" aria-hidden="true">
                  {entryKind === 'directory'
                    ? (isExpanded ? <FolderOpen size={16} /> : <Folder size={16} />)
                    : <FileText size={15} />}
                </span>
                <span className="entry-name">{entryLabel}</span>
                {entryKind === 'file' && <span className="entry-meta">{formatBytes(entry.size)}</span>}
              </Button>
              {entryKind === 'directory' && isExpanded && renderTree(entry.path, depth + 1)}
            </li>
          );
        })}
      </ul>
    );
  };

  const selectedDirectoryEntries = useMemo(() => (
    sortEntriesByTypeAndName(treeEntries[selectedDirectoryPath] || [])
  ), [selectedDirectoryPath, treeEntries]);
  const hasSelectedDirectoryEntries = Object.prototype.hasOwnProperty.call(treeEntries, selectedDirectoryPath);
  const selectedDirectoryLabel = selectedDirectoryPath
    ? selectedDirectoryPath.split('/').filter(Boolean).pop()
    : currentSliceDisplayName || currentSliceLabel || 'Files';

  const handleContentEntryClick = async (entry) => {
    const entryKind = normalizeEntryType(entry.type);
    if (entryKind === 'directory') {
      await openDirectoryPath(entry.path);
      return;
    }
    setFocusedEntry({ path: entry.path, type: 'file' });
    await openFilePath(entry.path, { size: entry.size });
  };
  const sidebarVisible = sidebarOpen || isSidebarDismissing;

  return (
    <section className="repo-browser repo-browser--with-tabs">
      <SliceDetailNav
        activeTab="code"
        sliceId={sliceId}
        sliceLabel={currentSliceDisplayName || currentSliceLabel}
        slice={currentSlice}
        publicApiBaseUrl={publicApiBaseUrl}
        onOpenCode={() => {}}
        onOpenCommits={onOpenCommits}
        onOpenChangesets={onOpenChangesets}
        onOpenAgents={onOpenAgents}
      />
      <div className="repo-main">
        <div
          className={`repo-layout${sidebarOpen ? '' : ' sidebar-collapsed'}${isResizingSidebar ? ' is-resizing-sidebar' : ''}`}
          style={{ '--repo-sidebar-width': `${sidebarWidth}px` }}
        >
          <div
            className={`sidebar-overlay${sidebarVisible ? ' visible' : ''}${isSidebarDismissing ? ' dismissing' : ''}`}
            onClick={closeSidebar}
          />
          <aside className={`repo-sidebar ${sidebarOpen ? 'open' : 'closed'}${isSidebarDismissing ? ' dismissing' : ''}`}>
            <div className="sidebar-content">
              <section className="sidebar-tree-section" aria-label="Selected slice files">
                <div className="sidebar-tree-header">
                  <div className="sidebar-tree-title">
                    <h2 className="sidebar-panel-title">File tree</h2>
                    <span title={currentSliceLabel}>{currentSliceDisplayName || 'Slice'}</span>
                  </div>
                  <div className="panel-header-actions">
                    <span
                      className={`tree-loading-indicator${isLoading ? ' visible' : ''}`}
                      role="status"
                      aria-live="polite"
                      aria-label={isLoading ? 'Loading repository content' : undefined}
                      data-testid="tree-loading-indicator"
                    >
                      <span className="tree-loading-dot" aria-hidden="true" />
                    </span>
                    {canShowSettings && (
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        className={`slice-settings-toggle ${viewingSettings ? 'active' : ''}`}
                        onClick={viewingSettings ? openFilesView : openSettingsView}
                        aria-label={viewingSettings ? 'Close slice settings' : 'Open slice settings'}
                        title={viewingSettings ? 'Close slice settings' : 'Slice settings'}
                        data-testid="repo-view-settings"
                      >
                        <Settings size={16} aria-hidden="true" />
                      </Button>
                    )}
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon"
                      className="sidebar-toggle"
                      onClick={closeSidebar}
                      aria-label="Close sidebar"
                      title="Close sidebar"
                    >
                      <PanelLeftClose size={16} aria-hidden="true" />
                    </Button>
                  </div>
                </div>
                {visibleEntryError && <div className="panel-error">{visibleEntryError}</div>}
                {!canLoad && <div className="panel-empty">Choose a slice to browse files.</div>}
                {canLoad && !isLoading && !visibleEntryError && hasLoadedRootEntries && (treeEntries[''] || []).length === 0 && (
                  <div className="panel-empty">No entries found.</div>
                )}
                {canLoad && renderTree('')}
              </section>
            </div>
            <div
              className="sidebar-resize-handle"
              role="separator"
              aria-label="Resize sidebar"
              aria-orientation="vertical"
              aria-valuemin={SIDEBAR_WIDTH_MIN}
              aria-valuemax={SIDEBAR_WIDTH_MAX}
              aria-valuenow={sidebarWidth}
              tabIndex={sidebarOpen ? 0 : -1}
              onPointerDown={startSidebarResize}
              onKeyDown={handleSidebarResizeKeyDown}
            />
          </aside>

          <div className="repo-code">
            <div className="code-header">
              <div className="code-header-left">
                {!sidebarOpen && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="sidebar-toggle open-btn"
                    onClick={openSidebar}
                    aria-label="Open sidebar"
                    title="Open file tree"
                    data-testid="sidebar-toggle"
                  >
                    <PanelLeftOpen size={17} aria-hidden="true" />
                  </Button>
                )}
                <div className="breadcrumbs">
                  {visibleBreadcrumbs.map((crumb, index) => {
                    const isSlicePrefix = index === 0;
                    const hasPathAfterPrefix = visibleBreadcrumbs.length > 1;
                    const separator = isSlicePrefix ? (hasPathAfterPrefix ? '://' : '') : (index < visibleBreadcrumbs.length - 1 ? '/' : '');
                    const isSelectedFileCrumb = Boolean(
                      selectedFile
                      && normalizeWorkspaceResultPath(crumb.path) === normalizeWorkspaceResultPath(selectedFile),
                    );
                    return (
                      <Button
                        key={`${crumb.path || 'slice-root'}-${index}`}
                        type="button"
                        variant="ghost"
                        className="breadcrumb"
                        onClick={() => handleBreadcrumbClick(crumb.path)}
                        title={
                          isSelectedFileCrumb
                            ? 'Open containing folder'
                            : (crumb.name === '…' ? 'Jump to parent folder' : crumb.name)
                        }
                      >
                        <span className="breadcrumb-label">{crumb.name}</span>
                        {separator && <span className="separator">{separator}</span>}
                      </Button>
                    );
                  })}
                </div>
              </div>
              <div className="code-header-actions">
                {selectedFile && !isCompactHeader && (
                  <span className="status file-size-status">
                    {displayedFileSize === null ? '' : formatBytes(displayedFileSize)}
                  </span>
                )}
                {!isCompactHeader && renderFileActions()}
                {isCompactHeader && selectedFile && (
                  <div className="header-actions-menu" ref={actionMenuRef}>
                    <Button
                      type="button"
                      variant="secondary"
                      className="history-toggle header-actions-menu-trigger"
                      onClick={() => setIsActionMenuOpen((value) => !value)}
                      aria-haspopup="menu"
                      aria-expanded={isActionMenuOpen}
                      title="More actions"
                    >
                      <Menu size={16} aria-hidden="true" />
                    </Button>
                    {isActionMenuOpen && (
                      <div className="header-actions-menu-dropdown" role="menu">
                        <span className="header-actions-menu-status">
                          {displayedFileSize === null ? '' : formatBytes(displayedFileSize)}
                        </span>
                        {renderFileActions(() => setIsActionMenuOpen(false))}
                      </div>
                    )}
                  </div>
                )}
              </div>
            </div>
            <div className="code-content">
              {!selectedFile && (
                <div className="folder-preview" data-testid="folder-preview">
                  <div className="folder-preview-header">
                    <div>
                      <h3>{selectedDirectoryLabel}</h3>
                      <span>{selectedDirectoryPath ? `/${selectedDirectoryPath}` : 'Slice root'}</span>
                    </div>
                    <span className="folder-preview-count">
                      {hasSelectedDirectoryEntries ? `${selectedDirectoryEntries.length} item${selectedDirectoryEntries.length === 1 ? '' : 's'}` : 'Loading'}
                    </span>
                  </div>
                  {!hasSelectedDirectoryEntries && isLoading && (
                    <div className="folder-preview-loading" role="status" aria-live="polite">
                      <span className="file-loading-spinner" aria-hidden="true" />
                      <span>Loading folder...</span>
                    </div>
                  )}
                  {!hasSelectedDirectoryEntries && !isLoading && visibleEntryError && <div className="panel-error">{visibleEntryError}</div>}
                  {hasSelectedDirectoryEntries && selectedDirectoryEntries.length === 0 && (
                    <div className="panel-empty">This folder is empty.</div>
                  )}
                  {hasSelectedDirectoryEntries && selectedDirectoryEntries.length > 0 && (
                    <ul className="folder-preview-list">
                      {selectedDirectoryEntries.map((entry) => {
                        const entryKind = normalizeEntryType(entry.type);
                        const entryLabel = getEntryName(entry);
                        return (
                          <li key={entry.path}>
                            <button
                              type="button"
                              className={`folder-preview-entry ${entryKind}`}
                              onClick={() => handleContentEntryClick(entry)}
                              title={getEntryDisplayPath(entry)}
                            >
                              <span className="folder-preview-entry-icon" aria-hidden="true">
                                {entryKind === 'directory' ? <Folder size={17} /> : <FileText size={16} />}
                              </span>
                              <span className="folder-preview-entry-name">{entryLabel}</span>
                              <span className="folder-preview-entry-meta">
                                {entryKind === 'directory' ? 'Folder' : formatBytes(entry.size)}
                              </span>
                            </button>
                          </li>
                        );
                      })}
                    </ul>
                  )}
                </div>
              )}
              {selectedFile && !showHistory && !isSelectedFileLoading && fileError && <div className="panel-error">{fileError}</div>}
              {selectedFile && !showHistory && (
                ((isSelectedFileLoading && hasPreviewContent) || (!isSelectedFileLoading && !fileError)) && (
                  isEditingFile ? (
                    <textarea
                      className="file-editor"
                      value={draftContent}
                      onChange={(event) => setDraftContent(event.target.value)}
                      spellCheck={false}
                    />
                  ) : previewMeta.mode === 'image' ? (
                    <div className="media-preview-wrapper">
                      <img className="media-preview-image" src={previewMeta.src} alt={previewPath} />
                    </div>
                  ) : previewMeta.mode === 'pdf' ? (
                    <iframe
                      className="media-preview-pdf"
                      src={previewMeta.src}
                      title={`${previewPath} PDF preview`}
                    />
                  ) : previewMeta.mode === 'markdown' ? (
                    <article
                      className="file-preview file-preview-markdown"
                      dangerouslySetInnerHTML={{ __html: markdownContent || '<p>File is empty.</p>' }}
                    />
                  ) : (
                    <div
                      className="file-preview"
                      onClick={(e) => {
                        const btn = e.target.closest('.fold-toggle');
                        if (!btn) return;
                        const lineNum = parseInt(btn.dataset.foldLine, 10);
                        if (!lineNum) return;
                        const table = e.currentTarget.querySelector('.code-table');
                        if (!table) return;
                        const isFolded = btn.classList.toggle('folded');
                        const rows = table.querySelectorAll('tr.code-line');
                        for (const row of rows) {
                          const range = (row.dataset.foldRange || '');
                          if (!range) continue;
                          const ranges = range.split(' ');
                          for (const r of ranges) {
                            const [start, end] = r.split('-').map(Number);
                            if (start === lineNum) {
                              const rowLine = parseInt(row.dataset.line, 10);
                              if (rowLine > start && rowLine <= end) {
                                row.classList.toggle('folded', isFolded);
                              }
                            }
                          }
                        }
                      }}
                    >
                      <table className="code-table" dangerouslySetInnerHTML={{ __html: highlightedContent || '<tr><td class="line-number"></td><td class="line-content">File is empty.</td></tr>' }} />
                    </div>
                  )
                )
              )}
              {selectedFile && showHistory && (
                <div className="history-panel" data-testid="history-panel">
                  {historyLoading && <div className="history-loading">Loading history...</div>}
                  {historyError && <div className="panel-error">{historyError}</div>}
                  {!historyLoading && !historyError && fileHistory.length === 0 && (
                    <div className="panel-empty">No history available for this file.</div>
                  )}
                  {!historyLoading && fileHistory.length > 0 && (
                    <ul className="history-list">
                      {fileHistory.map((change) => (
                        <li key={change.id} className="history-item" data-testid="history-item">
                          <div className="history-item-header">
                            <span className={`change-type change-type-${normalizeChangeType(change.change_type)}`}>
                              {formatChangeType(change.change_type)}
                            </span>
                            <a
                              className="commit-hash commit-diff-link"
                              title={change.commit_hash}
                              href="#"
                              data-testid="commit-diff-link"
                              onClick={(e) => {
                                e.preventDefault();
                                if (change.commit_hash && onNavigateToDiff) {
                                  onNavigateToDiff(change.commit_hash);
                                }
                              }}
                            >
                              {change.commit_hash ? change.commit_hash.slice(0, 7) : 'unknown'}
                            </a>
                          </div>
                          <div className="history-item-message">{change.message || 'No message'}</div>
                          <div className="history-item-meta">
                            <span className="history-author">{change.author || 'Unknown'}</span>
                            <span className="history-date">{formatTimestamp(change.timestamp)}</span>
                            {(change.lines_added > 0 || change.lines_deleted > 0) && (
                              <span className="history-lines">
                                <span className="lines-added">+{change.lines_added || 0}</span>
                                <span className="lines-deleted">-{change.lines_deleted || 0}</span>
                              </span>
                            )}
                          </div>
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
              )}
            </div>
          </div>
        </div>
        {viewingSettings && (
          <div
            className="slice-settings-modal-backdrop"
            role="presentation"
            onClick={openFilesView}
          >
            <div
              className="slice-settings-modal"
              role="dialog"
              aria-modal="true"
              aria-label="Slice settings"
              onClick={(event) => event.stopPropagation()}
            >
              <Button
                type="button"
                variant="ghost"
                size="icon"
                className="slice-settings-modal-close"
                onClick={openFilesView}
                aria-label="Close slice settings"
                title="Close slice settings"
              >
                <X size={17} aria-hidden="true" />
              </Button>
              <SliceSettings
                sliceId={sliceId}
                sliceName={currentSliceLabel}
                folderMounts={currentSlice?.folder_mounts}
                onFolderMountsChange={(updatedMounts) => {
                  if (currentSlice) {
                    currentSlice.folder_mounts = updatedMounts;
                  }
                }}
              />
            </div>
          </div>
        )}
      </div>
    </section>
  );
}
