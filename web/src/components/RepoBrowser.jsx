import { useCallback, useMemo, useState } from 'react';

import { apiBaseUrl } from '../utils/api.js';
import {
  buildBrowserEntriesUrl,
  buildBrowserFileUrl,
  buildBrowserRawFileUrl,
} from '../features/browser/browserApi.js';
import { useBrowserSidebar } from '../features/browser/useBrowserSidebar.js';
import { useRepoBrowserChrome } from '../features/browser/useRepoBrowserChrome.js';
import { useRepoBrowserData } from '../features/browser/useRepoBrowserData.js';
import { useInitialBrowserState, useRepoBrowserSlice } from '../features/browser/useRepoBrowserSlice.js';
import SliceDetailNav from './SliceDetailNav.jsx';
import RepoBrowserHeader from './browser/RepoBrowserHeader.jsx';
import RepoBrowserSidebar from './browser/RepoBrowserSidebar.jsx';
import RepoFileHistoryPanel from './browser/RepoFileHistoryPanel.jsx';
import RepoFileViewer from './browser/RepoFileViewer.jsx';
import RepoFolderPreview from './browser/RepoFolderPreview.jsx';

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
  onOpenSettings,
  refreshHistoryToken,
  isActive,
  slicesLoading,
  openFileRequest,
  initialBrowserData,
}) {
  const initialBrowserState = useInitialBrowserState();
  const initialRouteSliceHash = initialBrowserState?.sliceHash || '';
  const initialDataMatchesRawSlice = initialBrowserData?.selectedSliceId === currentSliceId
    && (
      !initialRouteSliceHash
      || String(initialBrowserData?.sliceHash || '') === String(initialRouteSliceHash)
    );
  const initialResolvedSliceHash = initialDataMatchesRawSlice
    ? initialBrowserData?.sliceHash || initialRouteSliceHash
    : initialRouteSliceHash;
  const initialSelectedFilePayload = initialDataMatchesRawSlice ? initialBrowserData?.selectedFilePayload : null;
  const initialSelectedFilePath = initialDataMatchesRawSlice
    ? initialBrowserData?.selectedFile || initialBrowserState?.file || ''
    : initialBrowserState?.file || '';
  const initialSelectedDirectoryPath = initialSelectedFilePath
    ? ''
    : String(initialBrowserState?.dir || '').replace(/^\/+/, '');
  const hasInitialSelectedFilePayload = Boolean(initialSelectedFilePayload?.content);
  const [sliceHash, setSliceHash] = useState(initialResolvedSliceHash);
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
  const {
    buildRoutePath,
    canLoad,
    canShowSettings,
    currentSlice,
    currentSliceDisplayName,
    currentSliceLabel,
    initialBrowserDataMatches,
    sliceId,
    treeEntriesScopeKey,
  } = useRepoBrowserSlice({
    authUsername,
    currentSliceId,
    initialBrowserData,
    initialBrowserState,
    onSliceChange,
    sliceHash,
    slices,
    slicesLoading,
  });
  const {
    actionMenuRef,
    closeCompactActions,
    isActionMenuOpen,
    openFilesView,
    toggleActionMenu,
  } = useRepoBrowserChrome({
    isCompactHeader,
  });

  const buildEntriesUrl = useCallback((path) => {
    return buildBrowserEntriesUrl({
      apiBaseUrl,
      sliceId,
      path,
      sliceHash,
    });
  }, [sliceHash, sliceId]);

  const buildFileUrl = useCallback((filePath) => {
    return buildBrowserFileUrl({
      apiBaseUrl,
      sliceId,
      filePath,
      sliceHash,
    });
  }, [sliceHash, sliceId]);

  const buildRawFileUrl = useCallback((filePath) => {
    return buildBrowserRawFileUrl({
      sliceId,
      filePath,
      sliceHash,
    });
  }, [sliceHash, sliceId]);

  const {
    activeBrowserPath,
    cancelFileEdit,
    confirmFileEdit,
    displayedFileSize,
    draftContent,
    expandedPaths,
    fileError,
    fileHistory,
    focusedEntry,
    getCreateTreeEntryBlockedReason,
    handleBreadcrumbClick,
    handleContentEntryClick,
    handleEntryClick,
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
  } = useRepoBrowserData({
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
  });

  const visibleBreadcrumbs = useMemo(() => {
    const slicePrefix = currentSliceLabel || 'slice';
    const breadcrumbs = activeBrowserPath
      ? [
          { name: slicePrefix, path: '' },
          ...activeBrowserPath.split('/').map((part, index, parts) => ({
            name: part,
            path: parts.slice(0, index + 1).join('/'),
          })),
        ]
      : [{ name: slicePrefix, path: '' }];
    const maxBreadcrumbs = isCompactHeader ? 4 : 8;
    if (breadcrumbs.length <= maxBreadcrumbs) {
      return breadcrumbs;
    }

    const trailingCount = Math.max(maxBreadcrumbs - 2, 2);
    const ellipsisTarget = breadcrumbs[breadcrumbs.length - trailingCount - 1];
    return [
      breadcrumbs[0],
      { name: '\u2026', path: ellipsisTarget?.path || '' },
      ...breadcrumbs.slice(-trailingCount),
    ];
  }, [activeBrowserPath, currentSliceLabel, isCompactHeader]);

  const sidebarVisible = sidebarOpen || isSidebarDismissing;
  const fileActionProps = useMemo(() => ({
    canEdit: Boolean(String(authUsername || '').trim()),
    isEditingFile,
    onCancelEdit: cancelFileEdit,
    onCommitEdit: confirmFileEdit,
    onCompactActionDone: closeCompactActions,
    onOpenRawFile: openRawFile,
    onShowEdit: showFileEditor,
    onToggleHistory: toggleHistory,
    selectedFile,
    showHistory,
  }), [
    authUsername,
    cancelFileEdit,
    closeCompactActions,
    confirmFileEdit,
    isEditingFile,
    openRawFile,
    selectedFile,
    showFileEditor,
    showHistory,
    toggleHistory,
  ]);

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
        onOpenSettings={onOpenSettings}
      />
      <div className="repo-main">
        <div
          className={`repo-layout${sidebarOpen ? '' : ' sidebar-collapsed'}${isResizingSidebar ? ' is-resizing-sidebar' : ''}`}
          style={{ '--repo-sidebar-width': `${sidebarWidth}px` }}
        >
          <RepoBrowserSidebar
            canLoad={canLoad}
            canShowSettings={canShowSettings}
            currentSliceDisplayName={currentSliceDisplayName}
            currentSliceLabel={currentSliceLabel}
            expandedPaths={expandedPaths}
            focusedEntry={focusedEntry}
            handleSidebarResizeKeyDown={handleSidebarResizeKeyDown}
            hasLoadedRootEntries={hasLoadedRootEntries}
            isLoading={isLoading}
            isSidebarDismissing={isSidebarDismissing}
            onCloseSidebar={closeSidebar}
            getCreateTreeEntryBlockedReason={getCreateTreeEntryBlockedReason}
            onEntryClick={handleEntryClick}
            onTreeAction={handleTreeAction}
            onOpenSettingsView={onOpenSettings}
            sidebarOpen={sidebarOpen}
            sidebarVisible={sidebarVisible}
            sidebarWidth={sidebarWidth}
            startSidebarResize={startSidebarResize}
            treeActionState={treeActionState}
            treeEntries={treeEntries}
            visibleEntryError={visibleEntryError}
          />

          <div className="repo-code">
            <RepoBrowserHeader
              actionMenuRef={actionMenuRef}
              displayedFileSize={displayedFileSize}
              fileActionProps={fileActionProps}
              isActionMenuOpen={isActionMenuOpen}
              isCompactHeader={isCompactHeader}
              onBreadcrumbClick={handleBreadcrumbClick}
              onOpenSidebar={openSidebar}
              onToggleActionMenu={toggleActionMenu}
              selectedFile={selectedFile}
              sidebarOpen={sidebarOpen}
              visibleBreadcrumbs={visibleBreadcrumbs}
            />
            <div className="code-content">
              {!selectedFile && (
                <RepoFolderPreview
                  hasSelectedDirectoryEntries={hasSelectedDirectoryEntries}
                  isLoading={isLoading}
                  onEntryClick={handleContentEntryClick}
                  selectedDirectoryEntries={selectedDirectoryEntries}
                  selectedDirectoryLabel={selectedDirectoryLabel}
                  selectedDirectoryPath={selectedDirectoryPath}
                  visibleEntryError={visibleEntryError}
                />
              )}
              <RepoFileViewer
                draftContent={draftContent}
                fileError={fileError}
                hasPreviewContent={hasPreviewContent}
                highlightedContent={highlightedContent}
                isEditingFile={isEditingFile}
                isSelectedFileLoading={isSelectedFileLoading}
                markdownContent={markdownContent}
                onDraftContentChange={setDraftContent}
                previewMeta={previewMeta}
                previewPath={previewPath}
                selectedFile={selectedFile}
                showHistory={showHistory}
              />
              <RepoFileHistoryPanel
                fileHistory={fileHistory}
                historyError={historyError}
                historyLoading={historyLoading}
                onNavigateToDiff={onNavigateToDiff}
                selectedFile={selectedFile}
                showHistory={showHistory}
              />
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
