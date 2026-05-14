import {
  ExternalLink,
  FileDiff,
  GitPullRequest,
  PanelRightClose,
  RefreshCw,
} from 'lucide-react';

import {
  changeStatusLabel,
  localChangesSummaryText,
} from '../../features/agents/agentLocalChanges.js';
import { shortEntityId } from '../../features/agents/agentModels.js';
import { Button } from '../ui/button.jsx';

export default function SliceAgentsLocalChangesPanel({
  assistantStreaming,
  canExportChangeset,
  canSendInput,
  changesetExportLoading,
  changesetMessage,
  displayError,
  hasDirtyFiles,
  latestExportedChangesetId,
  localChanges,
  localChangesLoading,
  onChangesetMessageChange,
  onExportChangeset,
  onHide,
  onRefresh,
}) {
  return (
    <section className="slice-agents-local-changes" data-testid="slice-agents-local-changes">
      <div className="slice-agents-local-changes-header">
        <div className="slice-agents-local-changes-title">
          <FileDiff size={16} aria-hidden="true" />
          <div>
            <h2>Local changes</h2>
            <span>
              {localChangesLoading && !localChanges ? 'Checking' : localChangesSummaryText(localChanges)}
              {localChanges?.trackedChangesetId ? ` · tracked ${shortEntityId(localChanges.trackedChangesetId)}` : ''}
            </span>
          </div>
        </div>
        <div className="slice-agents-local-changes-actions">
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="slice-agents-icon-button"
            onClick={onRefresh}
            disabled={localChangesLoading}
            aria-label="Refresh local changes"
            title="Refresh local changes"
            data-testid="slice-agents-refresh-local-changes"
          >
            <RefreshCw size={15} aria-hidden="true" />
          </Button>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="slice-agents-icon-button"
            onClick={onHide}
            aria-label="Hide local changes"
            title="Hide local changes"
            data-testid="slice-agents-hide-local-changes"
          >
            <PanelRightClose size={15} aria-hidden="true" />
          </Button>
        </div>
      </div>
      {displayError && <div className="panel-error">{displayError}</div>}
      {localChangesLoading && !localChanges && !displayError && (
        <div className="slice-agents-local-clean" data-testid="slice-agents-local-checking">Checking local checkout...</div>
      )}
      {!localChangesLoading && !localChanges && !displayError && (
        <div className="slice-agents-local-clean" data-testid="slice-agents-local-not-loaded">Local changes not loaded.</div>
      )}
      {localChanges && localChanges.pathCount > 0 && (
        <ul className="slice-agents-local-file-list" data-testid="slice-agents-local-file-list">
          {localChanges.paths.map((entry) => (
            <li key={`${entry.status}-${entry.path}`} className="slice-agents-local-file">
              <span
                className={`slice-agents-local-file-status slice-agents-local-file-status--${entry.status.toLowerCase()}`}
                title={changeStatusLabel(entry.status)}
              >
                {entry.status}
              </span>
              <span className="slice-agents-local-file-path" title={entry.path}>{entry.path}</span>
            </li>
          ))}
          {localChanges.truncated && (
            <li className="slice-agents-local-file slice-agents-local-file--more">
              {localChanges.pathCount - localChanges.paths.length} more changed files
            </li>
          )}
        </ul>
      )}
      {localChanges && localChanges.pathCount === 0 && (
        <div className="slice-agents-local-clean" data-testid="slice-agents-local-clean">Working tree clean</div>
      )}
      {latestExportedChangesetId && (
        <a
          className="slice-agents-export-link"
          href={`/changesets/${encodeURIComponent(latestExportedChangesetId)}`}
          data-testid="slice-agents-exported-changeset"
        >
          <GitPullRequest size={14} aria-hidden="true" />
          <span>{shortEntityId(latestExportedChangesetId, 18)}</span>
          <ExternalLink size={13} aria-hidden="true" />
        </a>
      )}
      <div className="slice-agents-export-controls">
        <input
          className="slice-agents-export-message"
          value={changesetMessage}
          onChange={(event) => onChangesetMessageChange(event.target.value)}
          placeholder="Changeset message"
          disabled={!canSendInput || changesetExportLoading || assistantStreaming}
          data-testid="slice-agents-export-message"
        />
        <Button
          type="button"
          variant="default"
          size="sm"
          className="slice-agents-export-button"
          onClick={onExportChangeset}
          disabled={!canExportChangeset}
          title={hasDirtyFiles ? 'Export local changes to a changeset' : 'No local changes to export'}
          data-testid="slice-agents-export-changeset"
        >
          <GitPullRequest size={15} aria-hidden="true" />
          {changesetExportLoading ? 'Exporting' : localChanges?.trackedChangesetId ? 'Update changeset' : 'Export changeset'}
        </Button>
      </div>
    </section>
  );
}
