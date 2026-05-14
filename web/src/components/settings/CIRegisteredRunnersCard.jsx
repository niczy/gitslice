import { Ban, ShieldOff } from 'lucide-react';

import {
  formatDateTime,
  listText,
  pick,
  runnerId,
  runnerStatus,
  statusVariant,
} from './CISettingsHelpers.js';
import { DataError } from './CISettingsPrimitives.jsx';
import { Badge } from '../ui/badge.jsx';
import { Button } from '../ui/button.jsx';
import { Card, CardContent } from '../ui/card.jsx';

export default function CIRegisteredRunnersCard({
  actionRunnerId,
  error,
  onRevokeModeChange,
  onRunnerAction,
  revokeMode,
  runners,
}) {
  return (
    <Card className="border-border/70">
      <CardContent className="space-y-4 pt-6">
        <div className="flex flex-col gap-2 md:flex-row md:items-start md:justify-between">
          <div>
            <h4 className="text-base font-semibold">Registered runners</h4>
            <p className="status">Disable stops new leases. Revoke invalidates the runner credential.</p>
          </div>
          <div className="flex items-center gap-2">
            <select className="h-9 rounded-md border border-border bg-background px-2 text-sm" value={revokeMode} onChange={(event) => onRevokeModeChange(event.target.value)}>
              <option value="requeue">Requeue leased jobs</option>
              <option value="cancel">Cancel leased jobs</option>
              <option value="leave">Leave leased jobs</option>
            </select>
          </div>
        </div>
        <DataError>{error}</DataError>
        {!error && runners.length === 0 && <div className="panel-empty">No runners have registered yet.</div>}
        {runners.length > 0 && (
          <div className="overflow-auto">
            <table className="w-full min-w-[900px] text-left text-sm">
              <thead className="border-b border-border/70 text-xs uppercase text-muted-foreground">
                <tr>
                  <th className="py-2 pr-3">Runner</th>
                  <th className="py-2 pr-3">Status</th>
                  <th className="py-2 pr-3">Pool</th>
                  <th className="py-2 pr-3">Executor</th>
                  <th className="py-2 pr-3">Version</th>
                  <th className="py-2 pr-3">Last seen</th>
                  <th className="py-2 pr-3">Current job</th>
                  <th className="py-2 pr-3 text-right">Actions</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border/60">
                {runners.map((runner) => {
                  const id = runnerId(runner);
                  const status = runnerStatus(runner);
                  const currentJobID = pick(runner, 'current_job_id', 'currentJobId');
                  return (
                    <tr key={id}>
                      <td className="py-2 pr-3">
                        <a className="font-mono text-xs text-primary hover:underline" href={`/settings/ci/runners/${encodeURIComponent(id)}`}>{id}</a>
                        <div className="text-xs text-muted-foreground">{runner.name || 'unnamed'} - {listText(runner.labels)}</div>
                      </td>
                      <td className="py-2 pr-3"><Badge variant={statusVariant(status)}>{status}</Badge></td>
                      <td className="py-2 pr-3">{runner.pool || 'default'}</td>
                      <td className="py-2 pr-3">{runner.executor || 'unknown'}</td>
                      <td className="py-2 pr-3">{runner.version || 'unknown'}</td>
                      <td className="py-2 pr-3">{formatDateTime(pick(runner, 'last_seen_at', 'lastSeenAt'))}</td>
                      <td className="py-2 pr-3 font-mono text-xs">{currentJobID || 'none'}</td>
                      <td className="py-2 pr-3">
                        <div className="flex justify-end gap-2">
                          {status === 'disabled' ? (
                            <Button type="button" size="sm" variant="outline" disabled={actionRunnerId === id} onClick={() => onRunnerAction(id, 'enable')}>Enable</Button>
                          ) : (
                            <Button type="button" size="sm" variant="outline" disabled={actionRunnerId === id} onClick={() => onRunnerAction(id, 'disable')}>
                              <Ban className="h-4 w-4" />
                              Disable
                            </Button>
                          )}
                          <Button type="button" size="sm" variant="destructive" disabled={actionRunnerId === id} onClick={() => onRunnerAction(id, 'revoke')}>
                            <ShieldOff className="h-4 w-4" />
                            Revoke
                          </Button>
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
