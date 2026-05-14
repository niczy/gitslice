import { Button } from '../ui/button.jsx';
import { formatChangeType } from '../../utils/format.js';
import { normalizeChangeType } from '../../utils/normalize.js';

export function scrollDiffFileIntoView(fileKey, panelItemRefs, fileRefs, diffContentRef) {
  const panelItemEl = panelItemRefs.current[fileKey];
  if (panelItemEl) {
    const listEl = panelItemEl.closest('.diff-file-panel-list');
    if (listEl) {
      const itemTop = panelItemEl.offsetTop;
      const itemBottom = itemTop + panelItemEl.offsetHeight;
      const visibleTop = listEl.scrollTop;
      const visibleBottom = visibleTop + listEl.clientHeight;

      if (itemTop < visibleTop) {
        listEl.scrollTo({ top: itemTop, behavior: 'smooth' });
      } else if (itemBottom > visibleBottom) {
        listEl.scrollTo({ top: itemBottom - listEl.clientHeight, behavior: 'smooth' });
      }
    }
  }

  const fileEl = fileRefs.current[fileKey];
  const diffContentEl = diffContentRef.current;
  if (fileEl && diffContentEl) {
    const fileRect = fileEl.getBoundingClientRect();
    const containerRect = diffContentEl.getBoundingClientRect();
    const targetTop = diffContentEl.scrollTop + (fileRect.top - containerRect.top) - 16;
    diffContentEl.scrollTo({
      top: Math.max(targetTop, 0),
      behavior: 'smooth',
    });
  }
}

export function DiffTopBar({
  backLabel,
  backTestId,
  controls,
  eyebrow,
  meta = null,
  onBack,
  title,
  titleTestId,
}) {
  return (
    <div className="diff-top-bar">
      <Button type="button" variant="ghost" className="diff-back-btn" onClick={onBack} data-testid={backTestId}>
        {backLabel}
      </Button>
      <div className="diff-top-title">
        <p className="eyebrow">{eyebrow}</p>
        <h2 data-testid={titleTestId}>{title}</h2>
        {meta}
      </div>
      {controls}
    </div>
  );
}

export function DiffSummary({
  data,
  includeRenamed = false,
  testId,
}) {
  if (!data) {
    return null;
  }
  const filesAdded = data.files_added || data.filesAdded || 0;
  const filesModified = data.files_modified || data.filesModified || 0;
  const filesDeleted = data.files_deleted || data.filesDeleted || 0;
  const filesRenamed = data.files_renamed || data.filesRenamed || 0;

  return (
    <div className="diff-summary" data-testid={testId}>
      <span className="diff-stat diff-stat-added">+{filesAdded} added</span>
      <span className="diff-stat diff-stat-modified">{filesModified} modified</span>
      <span className="diff-stat diff-stat-deleted">-{filesDeleted} deleted</span>
      {includeRenamed && filesRenamed > 0 && (
        <span className="diff-stat diff-stat-renamed">{filesRenamed} renamed</span>
      )}
    </div>
  );
}

export function DiffViewToggle({
  onChange,
  splitButtonTestId,
  testId,
  unifiedButtonTestId,
  viewMode,
}) {
  return (
    <div className="diff-view-toggle" data-testid={testId}>
      <Button
        type="button"
        variant="ghost"
        className={`diff-view-btn ${viewMode === 'unified' ? 'diff-view-btn-active' : ''}`}
        onClick={() => onChange('unified')}
        data-testid={unifiedButtonTestId}
      >
        Unified
      </Button>
      <Button
        type="button"
        variant="ghost"
        className={`diff-view-btn ${viewMode === 'split' ? 'diff-view-btn-active' : ''}`}
        onClick={() => onChange('split')}
        data-testid={splitButtonTestId}
      >
        Side-by-side
      </Button>
    </div>
  );
}

export function getDiffFileKey(change) {
  return change.id || change.path;
}

export function getChangesetDiffFileKey(change) {
  return change.id || `${change.path}-${change.old_path || ''}`;
}

function getChangePathParts(change) {
  const path = String(change.path || '');
  return {
    dirPath: path.split('/').slice(0, -1).join('/'),
    fileName: path.split('/').pop(),
    path,
  };
}

export function DiffFilePanel({
  changes,
  getChangeType = (change) => change.change_type || change.changeType,
  getFileKey = getDiffFileKey,
  headerLabel,
  itemTestId,
  onFileSelect,
  panelItemRefs,
  selectedFileId,
  testId,
}) {
  return (
    <nav className="diff-file-panel" data-testid={testId}>
      <div className="diff-file-panel-header">{headerLabel}</div>
      <ul className="diff-file-panel-list">
        {changes.map((change) => {
          const fileKey = getFileKey(change);
          const { dirPath, fileName, path } = getChangePathParts(change);
          const normalizedType = normalizeChangeType(getChangeType(change));
          return (
            <li key={fileKey}>
              <Button
                ref={(el) => { panelItemRefs.current[fileKey] = el; }}
                type="button"
                variant="ghost"
                className={`diff-file-panel-item w-full justify-start ${selectedFileId === fileKey ? 'diff-file-panel-item-active' : ''}`}
                onClick={() => onFileSelect(fileKey)}
                title={path}
                data-testid={itemTestId}
              >
                <span className={`diff-file-panel-badge change-type-${normalizedType}`}>
                  {normalizedType.charAt(0).toUpperCase()}
                </span>
                <span className="diff-file-panel-name">
                  {dirPath && <span className="diff-file-panel-dir">{dirPath}/</span>}
                  {fileName}
                </span>
                {(change.lines_added > 0 || change.lines_deleted > 0) && (
                  <span className="diff-file-panel-stats">
                    {change.lines_added > 0 && <span className="lines-added">+{change.lines_added}</span>}
                    {change.lines_deleted > 0 && <span className="lines-deleted">-{change.lines_deleted}</span>}
                  </span>
                )}
              </Button>
            </li>
          );
        })}
      </ul>
    </nav>
  );
}

export function DiffFileItemHeader({
  change,
  normalizedType = normalizeChangeType(change.change_type || change.changeType),
  pathTestId,
}) {
  return (
    <>
      <div className="diff-file-header">
        <div className="diff-file-header-main">
          <span className={`change-type change-type-${normalizedType}`}>
            {formatChangeType(change.change_type || change.changeType)}
          </span>
          <span className="diff-file-path" data-testid={pathTestId}>{change.path}</span>
          {change.old_path && change.old_path !== change.path && (
            <span className="diff-file-old-path">(was: {change.old_path})</span>
          )}
        </div>
      </div>
      <div className="diff-file-stats">
        {(change.lines_added > 0 || change.lines_deleted > 0) && (
          <span className="history-lines">
            <span className="lines-added">+{change.lines_added || 0}</span>
            <span className="lines-deleted">-{change.lines_deleted || 0}</span>
          </span>
        )}
      </div>
    </>
  );
}
