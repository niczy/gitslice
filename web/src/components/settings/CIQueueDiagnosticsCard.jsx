import { AlertTriangle } from 'lucide-react';

import { jobId, jobImage, jobPool } from './CISettingsHelpers.js';
import { DataError } from './CISettingsPrimitives.jsx';
import { Badge } from '../ui/badge.jsx';
import { Card, CardContent } from '../ui/card.jsx';

export default function CIQueueDiagnosticsCard({
  error,
  queuedJobs,
  queueWarnings,
}) {
  return (
    <Card className="border-border/70">
      <CardContent className="space-y-4 pt-6">
        <div className="flex items-center justify-between gap-3">
          <div>
            <h4 className="text-base font-semibold">Queue diagnostics</h4>
            <p className="status">Warnings call out missing compatible capacity before users wait on CI.</p>
          </div>
          <AlertTriangle className="h-5 w-5 text-muted-foreground" />
        </div>
        <DataError>{error}</DataError>
        {queueWarnings.length === 0 && !error && (
          <div className="panel-empty">No queue compatibility warnings.</div>
        )}
        {queueWarnings.length > 0 && (
          <div className="space-y-3">
            {queueWarnings.map((warning) => (
              <div key={warning.key} className="rounded-md border border-destructive/30 bg-destructive/5 p-3">
                <div className="font-medium text-destructive">{warning.title}</div>
                <div className="mt-1 text-sm text-muted-foreground">{warning.detail}</div>
              </div>
            ))}
          </div>
        )}
        {queuedJobs.length > 0 && (
          <div className="space-y-2">
            <div className="text-sm font-medium">Queued jobs</div>
            <div className="max-h-72 overflow-auto rounded-md border border-border/70">
              {queuedJobs.map((job) => (
                <div key={jobId(job)} className="border-b border-border/60 p-3 last:border-b-0">
                  <div className="flex items-center justify-between gap-2">
                    <span className="font-mono text-xs">{jobId(job)}</span>
                    <Badge variant="outline">{jobPool(job)}</Badge>
                  </div>
                  <div className="mt-1 text-sm">{job.check_name || job.checkName || job.job_key || job.jobKey}</div>
                  <div className="mt-1 text-xs text-muted-foreground">{jobImage(job) || 'no image'} - {job.manifest_path || job.manifestPath || 'manifest unknown'}</div>
                </div>
              ))}
            </div>
          </div>
        )}
      </CardContent>
    </Card>
  );
}
