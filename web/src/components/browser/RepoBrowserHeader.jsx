import {
  Menu,
  PanelLeftOpen,
} from 'lucide-react';

import { formatBytes } from '../../utils/format.js';
import { normalizeWorkspaceResultPath } from '../../features/browser/browserApi.js';
import { Button } from '../ui/button.jsx';
import RepoBrowserFileActions from './RepoBrowserFileActions.jsx';

export default function RepoBrowserHeader({
  actionMenuRef,
  displayedFileSize,
  fileActionProps,
  isActionMenuOpen,
  isCompactHeader,
  onBreadcrumbClick,
  onOpenSidebar,
  onToggleActionMenu,
  selectedFile,
  sidebarOpen,
  visibleBreadcrumbs,
}) {
  return (
    <div className="code-header">
      <div className="code-header-left">
        {!sidebarOpen && (
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="sidebar-toggle open-btn"
            onClick={onOpenSidebar}
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
                onClick={() => onBreadcrumbClick(crumb.path)}
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
        {!isCompactHeader && <RepoBrowserFileActions {...fileActionProps} />}
        {isCompactHeader && selectedFile && (
          <div className="header-actions-menu" ref={actionMenuRef}>
            <Button
              type="button"
              variant="secondary"
              className="history-toggle header-actions-menu-trigger"
              onClick={onToggleActionMenu}
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
                <RepoBrowserFileActions {...fileActionProps} onActionDone={fileActionProps.onCompactActionDone} />
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
