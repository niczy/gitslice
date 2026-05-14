import {
  ChevronDown,
  ChevronRight,
  FileText,
  Folder,
  FolderOpen,
  PanelLeftClose,
  Settings,
} from 'lucide-react';

import {
  SIDEBAR_WIDTH_MAX,
  SIDEBAR_WIDTH_MIN,
} from '../../features/browser/browserConstants.js';
import {
  getEntryDisplayPath,
  getEntryName,
  sortEntriesByTypeAndName,
} from '../../features/browser/browserModel.js';
import { formatBytes } from '../../utils/format.js';
import { normalizeEntryType } from '../../utils/normalize.js';
import { Button } from '../ui/button.jsx';

function RepoBrowserTree({
  depth = 0,
  expandedPaths,
  focusedEntry,
  onEntryClick,
  path,
  treeEntries,
}) {
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
              onClick={() => onEntryClick(entry)}
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
            {entryKind === 'directory' && isExpanded && (
              <RepoBrowserTree
                depth={depth + 1}
                expandedPaths={expandedPaths}
                focusedEntry={focusedEntry}
                onEntryClick={onEntryClick}
                path={entry.path}
                treeEntries={treeEntries}
              />
            )}
          </li>
        );
      })}
    </ul>
  );
}

export default function RepoBrowserSidebar({
  canLoad,
  canShowSettings,
  currentSliceDisplayName,
  currentSliceLabel,
  expandedPaths,
  focusedEntry,
  handleSidebarResizeKeyDown,
  hasLoadedRootEntries,
  isLoading,
  isSidebarDismissing,
  onCloseSidebar,
  onEntryClick,
  onOpenFilesView,
  onOpenSettingsView,
  sidebarOpen,
  sidebarVisible,
  sidebarWidth,
  startSidebarResize,
  treeEntries,
  viewingSettings,
  visibleEntryError,
}) {
  return (
    <>
      <div
        className={`sidebar-overlay${sidebarVisible ? ' visible' : ''}${isSidebarDismissing ? ' dismissing' : ''}`}
        onClick={onCloseSidebar}
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
                    onClick={viewingSettings ? onOpenFilesView : onOpenSettingsView}
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
                  onClick={onCloseSidebar}
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
            {canLoad && (
              <RepoBrowserTree
                expandedPaths={expandedPaths}
                focusedEntry={focusedEntry}
                onEntryClick={onEntryClick}
                path=""
                treeEntries={treeEntries}
              />
            )}
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
    </>
  );
}
