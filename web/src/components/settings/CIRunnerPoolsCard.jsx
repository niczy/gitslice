import { Server } from 'lucide-react';

import { listText, numberPick } from './CISettingsHelpers.js';
import { DataError } from './CISettingsPrimitives.jsx';
import { Card, CardContent } from '../ui/card.jsx';

export default function CIRunnerPoolsCard({ error, runnerPools }) {
  return (
    <Card className="border-border/70">
      <CardContent className="space-y-4 pt-6">
        <div className="flex items-center justify-between gap-3">
          <div>
            <h4 className="text-base font-semibold">Runner pools</h4>
            <p className="status">Policy comes from <code>/{'{home}'}/.gitslice/ci.yaml</code>.</p>
          </div>
          <Server className="h-5 w-5 text-muted-foreground" />
        </div>
        <DataError>{error}</DataError>
        {!error && runnerPools.length === 0 && <div className="panel-empty">No runner pools are configured yet.</div>}
        {runnerPools.length > 0 && (
          <div className="overflow-auto">
            <table className="w-full min-w-[760px] text-left text-sm">
              <thead className="border-b border-border/70 text-xs uppercase text-muted-foreground">
                <tr>
                  <th className="py-2 pr-3">Pool</th>
                  <th className="py-2 pr-3">Executor</th>
                  <th className="py-2 pr-3">Labels</th>
                  <th className="py-2 pr-3">Images</th>
                  <th className="py-2 pr-3">Online</th>
                  <th className="py-2 pr-3">Busy</th>
                  <th className="py-2 pr-3">Queued</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border/60">
                {runnerPools.map((pool) => (
                  <tr key={pool.name}>
                    <td className="py-2 pr-3 font-medium">{pool.name}</td>
                    <td className="py-2 pr-3">{pool.executor || 'unknown'}</td>
                    <td className="py-2 pr-3">{listText(pool.labels)}</td>
                    <td className="py-2 pr-3">{listText(pool.allowed_images || pool.allowedImages)}</td>
                    <td className="py-2 pr-3 font-mono">{numberPick(pool, 'online_runners', 'onlineRunners')}</td>
                    <td className="py-2 pr-3 font-mono">{numberPick(pool, 'busy_runners', 'busyRunners')}</td>
                    <td className="py-2 pr-3 font-mono">{numberPick(pool, 'queued_jobs', 'queuedJobs')}</td>
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
