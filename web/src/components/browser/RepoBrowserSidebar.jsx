import { useEffect, useRef, useState } from 'react';
import {
  ChevronDown,
  ChevronRight,
  FileText,
  FilePlus,
  Folder,
  FolderPlus,
  FolderOpen,
  Menu,
  PanelLeftClose,
  Pencil,
  Settings,
  Trash2,
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
import { filterVisibleTreeEntries } from '../../features/browser/browserTreeOperations.js';
import { formatBytes } from '../../utils/format.js';
import { normalizeEntryType } from '../../utils/normalize.js';
import { Button } from '../ui/button.jsx';

function TreeActionMenu({
  busy,
  createDisabledReason = '',
  entry,
  isOpen,
  onAction,
  onToggle,
  showCreateActions,
}) {
  const entryKind = normalizeEntryType(entry?.type);
  const canCreate = showCreateActions || entryKind === 'directory';
  const menuLabel = entry?.path ? `Actions for ${getEntryDisplayPath(entry)}` : 'File tree actions';
  const createDisabled = Boolean(createDisabledReason);

  const handleActionClick = (event, action) => {
    event.stopPropagation();
    onToggle(false);
    onAction(action, entry || null);
  };

  return (
    <div className="tree-action-menu-wrap">
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className={`tree-action-toggle${isOpen ? ' active' : ''}`}
        aria-label={menuLabel}
        aria-haspopup="menu"
        aria-expanded={isOpen}
        title={menuLabel}
        disabled={busy}
        onClick={(event) => {
          event.stopPropagation();
          onToggle(!isOpen);
        }}
      >
        <Menu size={15} aria-hidden="true" />
      </Button>
      {isOpen && (
        <div className="tree-action-menu" role="menu" onClick={(event) => event.stopPropagation()}>
          {canCreate && (
            <>
              <button
                type="button"
                role="menuitem"
                className="tree-action-item"
                disabled={createDisabled}
                title={createDisabledReason || 'New file'}
                onClick={(event) => handleActionClick(event, 'create-file')}
              >
                <FilePlus size={14} aria-hidden="true" />
                <span>New file</span>
              </button>
              <button
                type="button"
                role="menuitem"
                className="tree-action-item"
                disabled={createDisabled}
                title={createDisabledReason || 'New folder'}
                onClick={(event) => handleActionClick(event, 'create-folder')}
              >
                <FolderPlus size={14} aria-hidden="true" />
                <span>New folder</span>
              </button>
            </>
          )}
          {entry?.path && (
            <>
              <button type="button" role="menuitem" className="tree-action-item" onClick={(event) => handleActionClick(event, 'rename')}>
                <Pencil size={14} aria-hidden="true" />
                <span>Rename</span>
              </button>
              <button type="button" role="menuitem" className="tree-action-item danger" onClick={(event) => handleActionClick(event, 'delete')}>
                <Trash2 size={14} aria-hidden="true" />
                <span>Delete</span>
              </button>
            </>
          )}
        </div>
      )}
    </div>
  );
}

function RepoBrowserTree({
  actionMenuOpenKey,
  busy,
  depth = 0,
  expandedPaths,
  focusedEntry,
  getCreateTreeEntryBlockedReason = () => '',
  onEntryClick,
  onTreeAction,
  onToggleActionMenu,
  path,
  treeEntries,
}) {
  const entries = sortEntriesByTypeAndName(filterVisibleTreeEntries(treeEntries[path] || []));
  return (
    <ul className="tree-list">
      {entries.map((entry) => {
        const entryKind = normalizeEntryType(entry.type);
        const isExpanded = expandedPaths.includes(entry.path);
        const entryLabel = getEntryName(entry);
        const menuKey = `entry:${entry.path}`;
        return (
          <li key={entry.path} className="tree-item">
            <div className={`tree-entry-row${focusedEntry?.path === entry.path ? ' active' : ''}`}>
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
              <TreeActionMenu
                busy={busy}
                createDisabledReason={getCreateTreeEntryBlockedReason(entry)}
                entry={entry}
                isOpen={actionMenuOpenKey === menuKey}
                onAction={onTreeAction}
                onToggle={(nextOpen) => onToggleActionMenu(nextOpen ? menuKey : '')}
                showCreateActions={entryKind === 'directory'}
              />
            </div>
            {entryKind === 'directory' && isExpanded && (
              <RepoBrowserTree
                actionMenuOpenKey={actionMenuOpenKey}
                busy={busy}
                depth={depth + 1}
                expandedPaths={expandedPaths}
                focusedEntry={focusedEntry}
                getCreateTreeEntryBlockedReason={getCreateTreeEntryBlockedReason}
                onEntryClick={onEntryClick}
                onTreeAction={onTreeAction}
                onToggleActionMenu={onToggleActionMenu}
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
  getCreateTreeEntryBlockedReason = () => '',
  handleSidebarResizeKeyDown,
  hasLoadedRootEntries,
  isLoading,
  isSidebarDismissing,
  onCloseSidebar,
  onEntryClick,
  onTreeAction,
  onOpenFilesView,
  onOpenSettingsView,
  sidebarOpen,
  sidebarVisible,
  sidebarWidth,
  startSidebarResize,
  treeActionState,
  treeEntries,
  viewingSettings,
  visibleEntryError,
}) {
  const [actionMenuOpenKey, setActionMenuOpenKey] = useState('');
  const actionMenuRootRef = useRef(null);
  const actionBusy = Boolean(treeActionState?.busy);

  useEffect(() => {
    if (!actionMenuOpenKey) {
      return undefined;
    }
    const closeMenu = (event) => {
      if (actionMenuRootRef.current && !actionMenuRootRef.current.contains(event.target)) {
        setActionMenuOpenKey('');
      }
    };
    document.addEventListener('pointerdown', closeMenu);
    return () => document.removeEventListener('pointerdown', closeMenu);
  }, [actionMenuOpenKey]);

  return (
    <>
      <div
        className={`sidebar-overlay${sidebarVisible ? ' visible' : ''}${isSidebarDismissing ? ' dismissing' : ''}`}
        onClick={onCloseSidebar}
      />
      <aside className={`repo-sidebar ${sidebarOpen ? 'open' : 'closed'}${isSidebarDismissing ? ' dismissing' : ''}`}>
        <div className="sidebar-content">
          <section className="sidebar-tree-section" aria-label="Selected slice files" ref={actionMenuRootRef}>
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
                <TreeActionMenu
                  busy={actionBusy}
                  createDisabledReason={getCreateTreeEntryBlockedReason(null)}
                  entry={null}
                  isOpen={actionMenuOpenKey === 'tree-root'}
                  onAction={onTreeAction}
                  onToggle={(nextOpen) => setActionMenuOpenKey(nextOpen ? 'tree-root' : '')}
                  showCreateActions
                />
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
            {treeActionState?.error && <div className="panel-error">{treeActionState.error}</div>}
            {!treeActionState?.error && visibleEntryError && <div className="panel-error">{visibleEntryError}</div>}
            {treeActionState?.message && <div className="panel-success">{treeActionState.message}</div>}
            {!canLoad && <div className="panel-empty">Choose a slice to browse files.</div>}
            {canLoad && !isLoading && !visibleEntryError && hasLoadedRootEntries && filterVisibleTreeEntries(treeEntries[''] || []).length === 0 && (
              <div className="panel-empty">No entries found.</div>
            )}
            {canLoad && (
              <RepoBrowserTree
                actionMenuOpenKey={actionMenuOpenKey}
                busy={actionBusy}
                expandedPaths={expandedPaths}
                focusedEntry={focusedEntry}
                getCreateTreeEntryBlockedReason={getCreateTreeEntryBlockedReason}
                onEntryClick={onEntryClick}
                onTreeAction={onTreeAction}
                onToggleActionMenu={setActionMenuOpenKey}
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
