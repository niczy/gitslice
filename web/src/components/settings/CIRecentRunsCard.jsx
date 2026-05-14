import {
  formatDateTime,
  pick,
  statusVariant,
} from './CISettingsHelpers.js';
import { DataError } from './CISettingsPrimitives.jsx';
import { Badge } from '../ui/badge.jsx';
import { Card, CardContent } from '../ui/card.jsx';

export default function CIRecentRunsCard({ ciRuns, error }) {
  return (
    <Card className="border-border/70">
      <CardContent className="space-y-4 pt-6">
        <div>
          <h4 className="text-base font-semibold">Recent CI runs</h4>
          <p className="status">The latest runs show exact changeset versions and plan hashes when available.</p>
        </div>
        <DataError>{error}</DataError>
        {!error && ciRuns.length === 0 && <div className="panel-empty">No CI runs yet.</div>}
        {ciRuns.length > 0 && (
          <div className="overflow-auto">
            <table className="w-full min-w-[900px] text-left text-sm">
              <thead className="border-b border-border/70 text-xs uppercase text-muted-foreground">
                <tr>
                  <th className="py-2 pr-3">Run</th>
                  <th className="py-2 pr-3">Status</th>
                  <th className="py-2 pr-3">Changeset</th>
                  <th className="py-2 pr-3">Version</th>
                  <th className="py-2 pr-3">Plan hash</th>
                  <th className="py-2 pr-3">Created</th>
                  <th className="py-2 pr-3">Jobs</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border/60">
                {ciRuns.map((run) => (
                  <tr key={pick(run, 'run_id', 'runId')}>
                    <td className="py-2 pr-3 font-mono text-xs">{pick(run, 'run_id', 'runId')}</td>
                    <td className="py-2 pr-3"><Badge variant={statusVariant(run.status)}>{run.status || 'unknown'}</Badge></td>
                    <td className="py-2 pr-3 font-mono text-xs">{pick(run, 'changeset_id', 'changesetId') || 'none'}</td>
                    <td className="py-2 pr-3 font-mono text-xs">{pick(run, 'changeset_version_id', 'changesetVersionId') || 'none'}</td>
                    <td className="py-2 pr-3 font-mono text-xs">{pick(run, 'plan_hash', 'planHash') || 'none'}</td>
                    <td className="py-2 pr-3">{formatDateTime(pick(run, 'created_at', 'createdAt'))}</td>
                    <td className="py-2 pr-3 font-mono">{(run.jobs || []).length}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
