import {
  formatDateTime,
  jobId,
  jobPool,
  jobStatus,
  pick,
  runnerStatus,
  statusVariant,
} from './CISettingsHelpers.js';
import { DataError, Metric } from './CISettingsPrimitives.jsx';
import { Badge } from '../ui/badge.jsx';
import { Card, CardContent } from '../ui/card.jsx';

export default function CIRunnerDetailCard({
  errors,
  runnerJobs,
  selectedRunner,
  settingsRunnerId,
}) {
  if (!settingsRunnerId) {
    return null;
  }

  return (
    <Card className="border-border/70" data-testid="settings-ci-runner-detail">
      <CardContent className="space-y-4 pt-6">
        <div>
          <h4 className="text-base font-semibold">Runner detail</h4>
          <p className="status">Recent jobs for <code>{settingsRunnerId}</code>.</p>
        </div>
        <DataError>{errors.selectedRunner || errors.runnerJobs}</DataError>
        {selectedRunner && (
          <div className="grid gap-3 md:grid-cols-4">
            <Metric label="Status" value={runnerStatus(selectedRunner)} />
            <Metric label="Pool" value={selectedRunner.pool || 'default'} />
            <Metric label="Executor" value={selectedRunner.executor || 'unknown'} />
            <Metric label="Current job" value={pick(selectedRunner, 'current_job_id', 'currentJobId') || 'none'} />
          </div>
        )}
        {runnerJobs.length === 0 && !errors.runnerJobs && <div className="panel-empty">No runner job history yet.</div>}
        {runnerJobs.length > 0 && (
          <div className="overflow-auto">
            <table className="w-full min-w-[760px] text-left text-sm">
              <thead className="border-b border-border/70 text-xs uppercase text-muted-foreground">
                <tr>
                  <th className="py-2 pr-3">Job</th>
                  <th className="py-2 pr-3">Check</th>
                  <th className="py-2 pr-3">Status</th>
                  <th className="py-2 pr-3">Pool</th>
                  <th className="py-2 pr-3">Started</th>
                  <th className="py-2 pr-3">Finished</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-border/60">
                {runnerJobs.map((job) => (
                  <tr key={jobId(job)}>
                    <td className="py-2 pr-3 font-mono text-xs">{jobId(job)}</td>
                    <td className="py-2 pr-3">{job.check_name || job.checkName || job.job_key || job.jobKey}</td>
                    <td className="py-2 pr-3"><Badge variant={statusVariant(jobStatus(job))}>{jobStatus(job)}</Badge></td>
                    <td className="py-2 pr-3">{jobPool(job)}</td>
                    <td className="py-2 pr-3">{formatDateTime(pick(job, 'started_at', 'startedAt'))}</td>
                    <td className="py-2 pr-3">{formatDateTime(pick(job, 'finished_at', 'finishedAt'))}</td>
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
