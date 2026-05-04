import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import {
  Check,
  ChevronDown,
  ChevronRight,
  Circle,
  Edit3,
  FileText,
  Folder,
  FolderOpen,
  GitBranch,
  History,
  Menu,
  PanelLeftClose,
  PanelLeftOpen,
  PanelRightClose,
  PanelRightOpen,
  Settings,
  X,
} from 'lucide-react';
import { apiBaseUrl, fetchWithAuth } from '../utils/api.js';
import { formatBytes } from '../utils/format.js';
import { formatChangeType, formatTimestamp } from '../utils/format.js';
import { normalizeChange, normalizeChangeType, normalizeEntryType } from '../utils/normalize.js';
import { decodeBase64, highlightCode } from '../utils/highlight.js';
import { renderMarkdownHtml } from '../utils/markdown.js';
import { getSliceDisplayName } from '../utils/slices.js';
import { buildBrowserPath, parseLocation } from '../utils/routing.js';
import SliceSettings from './SliceSettings.jsx';
import { Button } from './ui/button.jsx';

const IMAGE_EXTENSIONS = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'bmp', 'ico', 'avif']);

const IMAGE_MIME_TYPES = {
  png: 'image/png',
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  gif: 'image/gif',
  webp: 'image/webp',
  svg: 'image/svg+xml',
  bmp: 'image/bmp',
  ico: 'image/x-icon',
  avif: 'image/avif',
};

const SIDEBAR_WIDTH_MIN = 220;
const SIDEBAR_WIDTH_MAX = 560;
const SIDEBAR_WIDTH_DEFAULT = 260;
const SIDEBAR_WIDTH_STORAGE_KEY = 'gitslice.browser.sidebarWidth';
const useIsomorphicLayoutEffect = typeof window === 'undefined' ? useEffect : useLayoutEffect;

function clampSidebarWidth(value) {
  const numericValue = Number(value);
  if (!Number.isFinite(numericValue)) {
    return SIDEBAR_WIDTH_DEFAULT;
  }
  return Math.min(SIDEBAR_WIDTH_MAX, Math.max(SIDEBAR_WIDTH_MIN, Math.round(numericValue)));
}

const getFileExtension = (filePath) => {
  if (!filePath || !filePath.includes('.')) {
    return '';
  }
  return filePath.split('.').pop()?.toLowerCase() || '';
};

const getEntryName = (entry) => {
  const name = String(entry?.name || '').trim();
  if (name) {
    return name;
  }
  const path = String(entry?.path || '').replace(/^\/+|\/+$/g, '');
  if (!path) {
    return '/';
  }
  return path.split('/').pop() || path;
};

const getEntryDisplayPath = (entry) => {
  const path = String(entry?.path || '').replace(/^\/+/, '');
  return path ? `/${path}` : '/';
};

const getPreviewMeta = (filePath, encodedContent) => {
  const extension = getFileExtension(filePath);
  if (extension === 'pdf') {
    return {
      mode: 'pdf',
      src: `data:application/pdf;base64,${encodedContent}`,
    };
  }

  if (IMAGE_EXTENSIONS.has(extension)) {
    return {
      mode: 'image',
      src: `data:${IMAGE_MIME_TYPES[extension] || 'image/*'};base64,${encodedContent}`,
    };
  }

  if (extension === 'md' || extension === 'markdown') {
    return { mode: 'markdown', src: '' };
  }

  return { mode: 'text', src: '' };
};

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
  refreshHistoryToken,
  isActive,
  slicesLoading,
  openFileRequest,
}) {
  // Parse initial browser state from the current route on mount.
  const initialBrowserState = useMemo(() => {
    if (typeof window === 'undefined') {
      return null;
    }
    const route = parseLocation(window.location);
    return route.page === 'browser' ? route.browserState || null : null;
  }, []);

  const [sliceHash, setSliceHash] = useState(initialBrowserState?.sliceHash || '');
  const [treeEntries, setTreeEntries] = useState({});
  const [expandedPaths, setExpandedPaths] = useState(['']);
  const [selectedFile, setSelectedFile] = useState(null);
  const [fileContent, setFileContent] = useState('');
  const [encodedFileContent, setEncodedFileContent] = useState('');
  const [draftContent, setDraftContent] = useState('');
  const [fileDrafts, setFileDrafts] = useState({});
  const [isEditingFile, setIsEditingFile] = useState(false);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState('');
  const [fileError, setFileError] = useState('');
  const [focusedEntry, setFocusedEntry] = useState(() => (initialBrowserState?.file
    ? { path: initialBrowserState.file, type: 'file' }
    : { path: '', type: 'directory' }));
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [sidebarWidth, setSidebarWidth] = useState(SIDEBAR_WIDTH_DEFAULT);
  const [isResizingSidebar, setIsResizingSidebar] = useState(false);
  const [insightOpen, setInsightOpen] = useState(true);
  const [showHistory, setShowHistory] = useState(false);
  const [fileHistory, setFileHistory] = useState([]);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [historyError, setHistoryError] = useState('');
  const [isSettingsOpen, setIsSettingsOpen] = useState(false);
  const [isCompactHeader, setIsCompactHeader] = useState(false);
  const [isActionMenuOpen, setIsActionMenuOpen] = useState(false);

  // File to restore after root tree entries load (from URL hash)
  const pendingFileRef = useRef(initialBrowserState?.file || null);
  const hasAppliedInitialSliceRef = useRef(false);
  const actionMenuRef = useRef(null);
  const sidebarResizeRef = useRef(null);
  const hasLoadedSidebarWidthRef = useRef(false);
  const handledOpenFileRequestTokenRef = useRef(null);
  const highlightedContent = useMemo(() => highlightCode(fileContent), [fileContent]);
  const markdownContent = useMemo(() => renderMarkdownHtml(fileContent), [fileContent]);
  const previewMeta = useMemo(() => getPreviewMeta(selectedFile, encodedFileContent), [selectedFile, encodedFileContent]);
  const selectedFileName = useMemo(() => selectedFile?.split('/').pop() || 'No file selected', [selectedFile]);
  const selectedFileExtension = useMemo(() => getFileExtension(selectedFile) || 'folder', [selectedFile]);
  const selectedFileLineCount = useMemo(() => {
    if (!selectedFile || !fileContent) {
      return 0;
    }
    return fileContent.split('\n').length;
  }, [fileContent, selectedFile]);
  const selectedFileMode = previewMeta.mode === 'text' ? 'source' : previewMeta.mode;
  const pendingDraftCount = Object.keys(fileDrafts).length;
  const hasLoadedRootEntries = Object.prototype.hasOwnProperty.call(treeEntries, '');
  const pendingDraftEntries = useMemo(() => Object.entries(fileDrafts).map(([path, content]) => ({
    path,
    name: path.split('/').pop() || path,
    size: typeof content === 'string' ? content.length : 0,
  })), [fileDrafts]);

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
      (normalizedRequested === normalizedAuthUsername || normalizedRequested === `home.${normalizedAuthUsername}`)
    ) {
      return `home.${normalizedAuthUsername}`;
    }

    const candidateIds = [requested];
    if (requested.startsWith('home.')) {
      const suffix = requested.slice('home.'.length).trim();
      if (suffix) {
        candidateIds.push(`home.${suffix.toLowerCase()}`);
      }
    } else {
      candidateIds.push(`home.${requested.toLowerCase()}`);
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

  useEffect(() => {
    if (!rawSliceId || sliceId === rawSliceId) {
      return;
    }
    onSliceChange(sliceId);
  }, [onSliceChange, rawSliceId, sliceId]);

  const currentSlice = useMemo(() => {
    return slices.find((slice) => slice.slice_id === sliceId) || null;
  }, [slices, sliceId]);

  const canLoad = sliceId !== '' && (sliceId === 'root_slice' || Boolean(String(authUsername || '').trim()));

  const currentSliceLabel = useMemo(() => {
    if (currentSlice?.name) {
      return currentSlice.name;
    }
    return sliceId === 'root_slice' ? 'Root Slice' : sliceId;
  }, [currentSlice, sliceId]);

  const currentSliceDisplayName = useMemo(() => {
    return getSliceDisplayName(currentSliceLabel);
  }, [currentSliceLabel]);

  const sliceInitials = useMemo(() => {
    const source = currentSliceDisplayName || authUsername || 'GS';
    return source
      .split(/[\s._-]+/)
      .filter(Boolean)
      .slice(0, 2)
      .map((part) => part[0]?.toUpperCase())
      .join('') || 'GS';
  }, [authUsername, currentSliceDisplayName]);

  const canShowSettings = canLoad && !currentSlice?.is_root;
  const viewingSettings = isSettingsOpen && canShowSettings;

  const openFilesView = useCallback(() => {
    setIsSettingsOpen(false);
  }, []);

  const openSettingsView = useCallback(() => {
    setIsSettingsOpen(true);
  }, []);

  useIsomorphicLayoutEffect(() => {
    if (typeof window === 'undefined') {
      return;
    }
    setSidebarOpen(window.innerWidth > 900);
    setInsightOpen(window.innerWidth > 1180);
    setIsCompactHeader(window.innerWidth <= 920);
    try {
      const storedWidth = window.localStorage.getItem(SIDEBAR_WIDTH_STORAGE_KEY);
      if (storedWidth !== null) {
        setSidebarWidth(clampSidebarWidth(storedWidth));
      }
    } catch {
      // Keep the default width when localStorage is unavailable.
    }
    hasLoadedSidebarWidthRef.current = true;
  }, []);

  useEffect(() => {
    if (typeof window === 'undefined' || !hasLoadedSidebarWidthRef.current) {
      return;
    }
    try {
      window.localStorage.setItem(SIDEBAR_WIDTH_STORAGE_KEY, String(sidebarWidth));
    } catch {
      // Resizing still works for the current session.
    }
  }, [sidebarWidth]);

  const startSidebarResize = useCallback((event) => {
    if (event.button !== undefined && event.button !== 0) {
      return;
    }
    setSidebarOpen(true);
    sidebarResizeRef.current = {
      startX: event.clientX,
      startWidth: sidebarWidth,
    };
    setIsResizingSidebar(true);
    event.preventDefault();
  }, [sidebarWidth]);

  useEffect(() => {
    if (!isResizingSidebar || typeof window === 'undefined') {
      return undefined;
    }

    const handlePointerMove = (event) => {
      const resizeState = sidebarResizeRef.current;
      if (!resizeState) {
        return;
      }
      setSidebarWidth(clampSidebarWidth(resizeState.startWidth + event.clientX - resizeState.startX));
    };

    const stopResize = () => {
      sidebarResizeRef.current = null;
      setIsResizingSidebar(false);
    };

    window.addEventListener('pointermove', handlePointerMove);
    window.addEventListener('pointerup', stopResize);
    window.addEventListener('pointercancel', stopResize);
    return () => {
      window.removeEventListener('pointermove', handlePointerMove);
      window.removeEventListener('pointerup', stopResize);
      window.removeEventListener('pointercancel', stopResize);
    };
  }, [isResizingSidebar]);

  const handleSidebarResizeKeyDown = useCallback((event) => {
    const step = event.shiftKey ? 40 : 16;
    if (event.key === 'ArrowLeft') {
      event.preventDefault();
      setSidebarOpen(true);
      setSidebarWidth((width) => clampSidebarWidth(width - step));
    } else if (event.key === 'ArrowRight') {
      event.preventDefault();
      setSidebarOpen(true);
      setSidebarWidth((width) => clampSidebarWidth(width + step));
    } else if (event.key === 'Home') {
      event.preventDefault();
      setSidebarOpen(true);
      setSidebarWidth(SIDEBAR_WIDTH_MIN);
    } else if (event.key === 'End') {
      event.preventDefault();
      setSidebarOpen(true);
      setSidebarWidth(SIDEBAR_WIDTH_MAX);
    }
  }, []);

  useEffect(() => {
    if (!canShowSettings && isSettingsOpen) {
      setIsSettingsOpen(false);
    }
  }, [canShowSettings, isSettingsOpen]);

  useEffect(() => {
    setIsSettingsOpen(false);
  }, [sliceId, sliceHash]);

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

  const breadcrumbs = useMemo(() => {
    const slicePrefix = currentSliceLabel || 'slice';
    if (!selectedFile) {
      return [{ name: slicePrefix, path: '' }];
    }
    const parts = selectedFile.split('/');
    return [
      { name: slicePrefix, path: '' },
      ...parts.map((part, index) => ({
        name: part,
        path: parts.slice(0, index + 1).join('/'),
      })),
    ];
  }, [currentSliceLabel, selectedFile]);

  const visibleBreadcrumbs = useMemo(() => {
    const maxBreadcrumbs = isCompactHeader ? 3 : 5;
    if (breadcrumbs.length <= maxBreadcrumbs) {
      return breadcrumbs;
    }

    const trailingCount = Math.max(maxBreadcrumbs - 2, 1);
    const ellipsisTarget = breadcrumbs[breadcrumbs.length - trailingCount - 1];
    return [
      breadcrumbs[0],
      { name: '…', path: ellipsisTarget?.path || '' },
      ...breadcrumbs.slice(-trailingCount),
    ];
  }, [breadcrumbs, isCompactHeader]);

  useEffect(() => {
    if (typeof window === 'undefined') {
      return undefined;
    }
    const handleResize = () => {
      const width = window.innerWidth;
      setIsCompactHeader(width <= 920);
      setSidebarOpen((open) => (width > 900 ? open : false));
      setInsightOpen((open) => (width > 1180 ? open : false));
    };

    window.addEventListener('resize', handleResize);
    return () => window.removeEventListener('resize', handleResize);
  }, []);

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
    setTreeEntries({});
    setExpandedPaths(['']);
    setSelectedFile(null);
    setFileContent('');
    setEncodedFileContent('');
    setDraftContent('');
    setFileDrafts({});
    setIsEditingFile(false);
    setFileError('');
    setFocusedEntry({ path: '', type: 'directory' });
  }, [sliceId, sliceHash]);

  const encodePath = (value) => value.split('/').map(encodeURIComponent).join('/');

  // Build URL for entries endpoint based on mode
  const buildEntriesUrl = (path) => {
    const params = new URLSearchParams();
    const encodedPath = path ? encodePath(path) : '';
    const pathSuffix = encodedPath ? `/${encodedPath}` : '';

    if (sliceHash) {
      params.set('slice_version.slice_hash', sliceHash);
    }
    const queryString = params.toString();
    return `${apiBaseUrl}/v1/slices/${sliceId}/entries${pathSuffix}${queryString ? `?${queryString}` : ''}`;
  };

  // Build URL for file endpoint based on mode
  const buildFileUrl = (filePath) => {
    const encodedPath = filePath ? encodePath(filePath) : '';
    const pathSuffix = encodedPath ? `/${encodedPath}` : '';
    const params = new URLSearchParams();

    if (sliceHash) {
      params.set('slice_version.slice_hash', sliceHash);
    }
    const queryString = params.toString();
    return `${apiBaseUrl}/v1/slices/${sliceId}/files${pathSuffix}${queryString ? `?${queryString}` : ''}`;
  };

  // Build URL for file history endpoint based on mode
  const buildHistoryUrl = (filePath) => {
    const encodedPath = filePath ? encodePath(filePath) : '';
    const pathSuffix = encodedPath ? `/${encodedPath}` : '';
    return `${apiBaseUrl}/v1/slices/${sliceId}/files/history${pathSuffix}`;
  };

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
  }, [sliceId]);

  // Toggle history panel
  const toggleHistory = () => {
    const newShowHistory = !showHistory;
    setShowHistory(newShowHistory);
    if (newShowHistory && selectedFile && fileHistory.length === 0) {
      fetchFileHistory(selectedFile);
    }
  };

  const normalizeWorkspaceResultPath = useCallback((value) => String(value || '').replace(/^\/+/, ''), []);

  const openFilePath = useCallback(async (targetPath) => {
    const normalizedPath = normalizeWorkspaceResultPath(targetPath);
    if (!canLoad || !normalizedPath) {
      return;
    }

    if (typeof window !== 'undefined' && window.innerWidth <= 900) {
      setSidebarOpen(false);
    }

    setIsSettingsOpen(false);
    setSelectedFile(normalizedPath);
    setFileContent('');
    setEncodedFileContent('');
    setDraftContent('');
    setIsLoading(true);
    setError('');
    setFileError('');
    setShowHistory(false);
    setFileHistory([]);
    setHistoryError('');

    const nextTreeEntries = { ...treeEntries };
    const nextExpandedPaths = new Set(['']);

    for (const path of expandedPaths) {
      nextExpandedPaths.add(path);
    }

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

      if (Object.prototype.hasOwnProperty.call(fileDrafts, normalizedPath)) {
        setFileContent(fileDrafts[normalizedPath]);
        setEncodedFileContent('');
        setDraftContent(fileDrafts[normalizedPath]);
        setIsEditingFile(false);
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
      setDraftContent(decodedContent);
      setIsEditingFile(false);
    } catch (err) {
      setFileContent('');
      setEncodedFileContent('');
      setFileError(err?.message || 'Unable to load file content.');
    } finally {
      setIsLoading(false);
    }
  }, [buildEntriesUrl, buildFileUrl, canLoad, expandedPaths, fileDrafts, normalizeWorkspaceResultPath, treeEntries]);

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
              setFileContent(decodeBase64(content));
            }
          } catch (e) {
            if (active && e?.name !== 'AbortError') {
              setFileContent('');
              setEncodedFileContent('');
              setFileError(e?.message || 'Unable to load file content.');
            }
          }
        } else {
          setTreeEntries({ '': rootEntries });
        }
      } catch (err) {
        if (!active || err?.name === 'AbortError') {
          return;
        }
        setError('Unable to load entries. Confirm the file gateway is running and the slice exists.');
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
  }, [canLoad, isActive, sliceId, sliceHash, refreshHistoryToken]);

  useEffect(() => {
    if (!isActive || !canLoad || !selectedFile || isEditingFile) {
      return;
    }

    let active = true;
    const controller = new AbortController();

    const refreshSelectedFile = async () => {
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
        setDraftContent(decodedContent);
      } catch (err) {
        if (!active || err?.name === 'AbortError') {
          return;
        }
        setFileError(err?.message || 'Unable to load file content.');
      }
    };

    refreshSelectedFile();

    return () => {
      active = false;
      controller.abort();
    };
  }, [canLoad, isActive, isEditingFile, refreshHistoryToken, selectedFile, sliceId, sliceHash]);

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
      setError('Unable to load entries. Confirm the file gateway is running and the slice exists.');
    } finally {
      setIsLoading(false);
    }
  };

  const toggleDirectory = async (entry) => {
    const isExpanded = expandedPaths.includes(entry.path);
    if (isExpanded) {
      setExpandedPaths((prev) => prev.filter((path) => path !== entry.path));
      return;
    }

    if (!treeEntries[entry.path]) {
      await fetchEntries(entry.path);
    }

    setExpandedPaths((prev) => [...prev, entry.path]);
  };

  // Replace the current detail URL with file/hash state without overwriting the slice home entry.
  const pushBrowserState = useCallback((file) => {
    if (typeof window === 'undefined') {
      return;
    }
    const currentRoute = parseLocation(window.location);
    if (currentRoute.page !== 'browser' || !currentRoute.browserState?.slice) {
      return;
    }
    window.history.replaceState(null, '', buildBrowserPath({
      file,
      slice: sliceId,
      sliceHash,
    }));
  }, [sliceId, sliceHash]);

  // Update URL when browser state changes or page becomes active again
  useEffect(() => {
    if (isActive && sliceId) {
      pushBrowserState(selectedFile || '');
    }
  }, [isActive, pushBrowserState, selectedFile, sliceId, sliceHash]);

  const handleEntryClick = async (entry) => {
    const entryKind = normalizeEntryType(entry.type);
    setFocusedEntry({ path: entry.path, type: entryKind });
    if (entryKind === 'directory') {
      await toggleDirectory(entry);
      return;
    }
    await openFilePath(entry.path);
  };

  const confirmFileEdit = () => {
    if (!selectedFile) {
      return;
    }
    setFileDrafts((prev) => ({ ...prev, [selectedFile]: draftContent }));
    setFileContent(draftContent);
    setEncodedFileContent('');
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
    setFocusedEntry({ path, type: 'directory' });
    if (path && !treeEntries[path]) {
      await fetchEntries(path);
    }

    if (path) {
      setExpandedPaths((prev) => (prev.includes(path) ? prev : [...prev, path]));
      if (path !== selectedFile) {
        setSelectedFile(null);
        setFileContent('');
        setEncodedFileContent('');
        setDraftContent('');
        setFileError('');
        setShowHistory(false);
      }
      return;
    }

    setExpandedPaths(['']);
    setSelectedFile(null);
    setFileContent('');
    setEncodedFileContent('');
    setDraftContent('');
    setFileError('');
    setShowHistory(false);
  };

  const renderTree = (path, depth = 0) => {
    const entries = [...(treeEntries[path] || [])].sort((left, right) => {
      const leftType = normalizeEntryType(left.type);
      const rightType = normalizeEntryType(right.type);
      if (leftType !== rightType) {
        return leftType === 'directory' ? -1 : 1;
      }
      return getEntryName(left).localeCompare(getEntryName(right), undefined, { sensitivity: 'base' });
    });
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

  return (
    <section className="repo-browser">
      <div className="repo-main">
        <div
          className={`repo-layout${sidebarOpen ? '' : ' sidebar-collapsed'}${insightOpen ? '' : ' insight-collapsed'}${isResizingSidebar ? ' is-resizing-sidebar' : ''}`}
          style={{ '--repo-sidebar-width': `${sidebarWidth}px` }}
        >
          <div
            className={`sidebar-overlay${sidebarOpen ? ' visible' : ''}`}
            onClick={() => setSidebarOpen(false)}
          />
          <aside className={`repo-sidebar ${sidebarOpen ? 'open' : 'closed'}`}>
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
                      aria-label={isLoading ? 'Loading folders' : undefined}
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
                      onClick={() => setSidebarOpen(false)}
                      aria-label="Close sidebar"
                      title="Close sidebar"
                    >
                      <PanelLeftClose size={16} aria-hidden="true" />
                    </Button>
                  </div>
                </div>
                {error && <div className="panel-error">{error}</div>}
                {!canLoad && <div className="panel-empty">Choose a slice to browse files.</div>}
                {canLoad && !isLoading && !error && hasLoadedRootEntries && (treeEntries[''] || []).length === 0 && (
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
                    onClick={() => setSidebarOpen(true)}
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
                    return (
                      <Button
                        key={`${crumb.path || 'slice-root'}-${index}`}
                        type="button"
                        variant="ghost"
                        className="breadcrumb"
                        onClick={() => handleBreadcrumbClick(crumb.path)}
                        title={crumb.name === '…' ? 'Jump to parent folder' : crumb.name}
                      >
                        {crumb.name}
                        {separator && <span className="separator">{separator}</span>}
                      </Button>
                    );
                  })}
                </div>
              </div>
              <div className="code-header-actions">
                {selectedFile && !isCompactHeader && <span className="status">{formatBytes(fileContent.length)}</span>}
                {!isCompactHeader && renderFileActions()}
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className={`insight-toggle ${insightOpen ? 'active' : ''}`}
                  onClick={() => setInsightOpen((value) => !value)}
                  aria-label={insightOpen ? 'Collapse file insight' : 'Expand file insight'}
                  title={insightOpen ? 'Collapse file insight' : 'Expand file insight'}
                  data-testid="insight-toggle"
                >
                  {insightOpen ? <PanelRightClose size={17} aria-hidden="true" /> : <PanelRightOpen size={17} aria-hidden="true" />}
                </Button>
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
                        <span className="header-actions-menu-status">{formatBytes(fileContent.length)}</span>
                        {renderFileActions(() => setIsActionMenuOpen(false))}
                      </div>
                    )}
                  </div>
                )}
              </div>
            </div>
            <div className="code-content">
              {!selectedFile && <div className="panel-empty">Choose a file from the tree to preview its contents.</div>}
              {selectedFile && !showHistory && fileError && <div className="panel-error">{fileError}</div>}
              {selectedFile && !showHistory && (
                !fileError && (
                  isEditingFile ? (
                    <textarea
                      className="file-editor"
                      value={draftContent}
                      onChange={(event) => setDraftContent(event.target.value)}
                      spellCheck={false}
                    />
                  ) : previewMeta.mode === 'image' ? (
                    <div className="media-preview-wrapper">
                      <img className="media-preview-image" src={previewMeta.src} alt={selectedFile} />
                    </div>
                  ) : previewMeta.mode === 'pdf' ? (
                    <iframe
                      className="media-preview-pdf"
                      src={previewMeta.src}
                      title={`${selectedFile} PDF preview`}
                    />
                  ) : previewMeta.mode === 'markdown' ? (
                    <article
                      className="file-preview file-preview-markdown"
                      dangerouslySetInnerHTML={{ __html: markdownContent || '<p>File is empty.</p>' }}
                    />
                  ) : (
                    <pre className="file-preview">
                      <code dangerouslySetInnerHTML={{ __html: highlightedContent || 'File is empty.' }} />
                    </pre>
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
          <aside className={`repo-insight ${insightOpen ? 'open' : 'closed'}`} aria-label="File insight" aria-hidden={!insightOpen}>
            <div className="repo-insight-scroll">
              <div className="repo-insight-header">
                <h2>Details</h2>
                <span className="repo-insight-status">
                  <Circle size={8} fill="currentColor" aria-hidden="true" />
                  {sliceHash ? 'Snapshot' : 'Live'}
                </span>
              </div>

              <section className="inspector-card inspector-status-card" aria-label="Slice status">
                <div className="slice-avatar" aria-hidden="true">{sliceInitials}</div>
                <div className="slice-status-copy">
                  <span className="inspector-label">Slice Status</span>
                  <strong>{currentSliceDisplayName || 'Choose a slice'}</strong>
                  <span>{sliceHash ? 'Historical snapshot' : 'Live workspace'}</span>
                </div>
              </section>

              <section className="repo-insight-section">
                <div className="inspector-section-title">
                  <h3>Pending Changes</h3>
                  <span>{pendingDraftCount}</span>
                </div>
                {pendingDraftEntries.length > 0 ? (
                  <ul className="inspector-change-list">
                    {pendingDraftEntries.slice(0, 4).map((draft) => (
                      <li key={draft.path}>
                        <Edit3 size={14} aria-hidden="true" />
                        <span>{draft.name}</span>
                        <small>{formatBytes(draft.size)}</small>
                      </li>
                    ))}
                  </ul>
                ) : selectedFile ? (
                  <p className="repo-insight-copy">{selectedFileName} has no pending edits.</p>
                ) : (
                  <p className="repo-insight-copy">Select a file to inspect changes.</p>
                )}
              </section>

              <section className="repo-insight-section">
                <h3>Properties</h3>
                <dl className="repo-insight-meta">
                  <div>
                    <dt>File</dt>
                    <dd>{selectedFile || 'No file selected'}</dd>
                  </div>
                  <div className="repo-insight-pair">
                    <div>
                      <dt>Type</dt>
                      <dd>{selectedFile ? selectedFileExtension.toUpperCase() : 'None'}</dd>
                    </div>
                    <div>
                      <dt>Size</dt>
                      <dd>{selectedFile ? formatBytes(fileContent.length) : '—'}</dd>
                    </div>
                  </div>
                  <div className="repo-insight-pair">
                    <div>
                      <dt>Lines</dt>
                      <dd>{selectedFile ? selectedFileLineCount : '—'}</dd>
                    </div>
                    <div>
                      <dt>Mode</dt>
                      <dd>{selectedFile ? selectedFileMode : 'Browse'}</dd>
                    </div>
                  </div>
                </dl>
              </section>

              <section className="repo-insight-section repo-insight-history">
                <div className="inspector-section-title">
                  <h3>History</h3>
                  <GitBranch size={14} aria-hidden="true" />
                </div>
                {fileHistory.length > 0 ? (
                  <ul className="inspector-history-list">
                    {fileHistory.slice(0, 3).map((change) => (
                      <li key={change.id}>
                        <span className={`change-type change-type-${normalizeChangeType(change.change_type)}`}>
                          {formatChangeType(change.change_type)}
                        </span>
                        <p>{change.message || 'No message'}</p>
                        <small>{formatTimestamp(change.timestamp)}</small>
                      </li>
                    ))}
                  </ul>
                ) : selectedFile ? (
                  <p>Open history to load recent changes for {selectedFileName}.</p>
                ) : (
                  <p>Select a file to reveal its metadata.</p>
                )}
              </section>
            </div>
          </aside>
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
                slice={currentSlice}
                publicApiBaseUrl={publicApiBaseUrl}
              />
            </div>
          </div>
        )}
      </div>
    </section>
  );
}
