import {
  RefreshCw,
  X,
} from 'lucide-react';

import { Button } from '../ui/button.jsx';

export default function SliceAgentsRunnerInfoDialog({
  canRestartRunner,
  error,
  infoRows,
  onClose,
  onUpgradeRestart,
  runnerActionLoading,
  runnerRunningDir,
  summary,
}) {
  return (
    <div className="slice-agents-info-dialog-backdrop" role="presentation" onClick={onClose}>
      <div
        className="slice-agents-info-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="slice-agents-info-dialog-title"
        data-testid="slice-agents-info-dialog"
        onClick={(event) => event.stopPropagation()}
      >
        <div className="slice-agents-info-dialog-header">
          <div>
            <h2 id="slice-agents-info-dialog-title">Agent runner</h2>
            <span>{summary}</span>
          </div>
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="slice-agents-icon-button"
            onClick={onClose}
            aria-label="Close agent runner details"
            title="Close"
          >
            <X size={15} aria-hidden="true" />
          </Button>
        </div>
        {runnerRunningDir && (
          <div className="slice-agents-runner-dir" title={runnerRunningDir}>
            {runnerRunningDir}
          </div>
        )}
        <div className="slice-agents-info-panel" data-testid="slice-agents-info-panel">
          <dl>
            {infoRows.map(([label, value]) => (
              <div key={label} className="slice-agents-info-row">
                <dt>{label}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>
        </div>
        <div className="slice-agents-info-dialog-actions">
          <Button
            type="button"
            variant="default"
            size="sm"
            className="slice-agents-runner-action"
            onClick={onUpgradeRestart}
            disabled={!canRestartRunner || runnerActionLoading}
            title={canRestartRunner ? 'Upgrade and restart running agent' : 'Select an active session'}
            data-testid="slice-agents-upgrade-restart"
          >
            <RefreshCw size={15} aria-hidden="true" />
            {runnerActionLoading ? 'Requesting' : 'Upgrade & restart'}
          </Button>
        </div>
        {error && <div className="panel-error">{error}</div>}
      </div>
    </div>
  );
}
