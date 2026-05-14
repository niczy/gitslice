import {
  formatChangeType,
  formatTimestamp,
} from '../../utils/format.js';
import { normalizeChangeType } from '../../utils/normalize.js';

export default function RepoFileHistoryPanel({
  fileHistory,
  historyError,
  historyLoading,
  onNavigateToDiff,
  selectedFile,
  showHistory,
}) {
  if (!selectedFile || !showHistory) {
    return null;
  }

  return (
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
                  onClick={(event) => {
                    event.preventDefault();
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
  );
}
