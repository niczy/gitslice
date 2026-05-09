import { useEffect, useMemo, useRef, useState } from 'react';
import { AlertTriangle, Ban, Clipboard, RefreshCw, Server, ShieldOff } from 'lucide-react';

import { Badge } from './ui/badge.jsx';
import { Button } from './ui/button.jsx';
import { Card, CardContent } from './ui/card.jsx';
import {
  createRunnerToken,
  disableRunner,
  enableRunner,
  getRunner,
  listCIRuns,
  listQueuedJobs,
  listRunnerJobs,
  listRunnerPools,
  listRunners,
  revokeRunner,
} from '../utils/api.js';
import { copyToClipboard } from '../utils/clipboard.js';

function pick(value, snakeName, camelName, fallback = '') {
  return value?.[snakeName] ?? value?.[camelName] ?? fallback;
}

function numberPick(value, snakeName, camelName, fallback = 0) {
  const next = Number(pick(value, snakeName, camelName, fallback));
  return Number.isFinite(next) ? next : fallback;
}

function statusVariant(status) {
  const normalized = String(status || '').toLowerCase();
  if (['idle', 'online', 'passed', 'success', 'active'].includes(normalized)) {
    return 'secondary';
  }
  if (['failed', 'error', 'disabled', 'revoked', 'cancelled'].includes(normalized)) {
    return 'destructive';
  }
  return 'outline';
}

function formatDateTime(value) {
  const raw = String(value || '').trim();
  if (!raw) return 'unknown';
  const date = new Date(raw);
  if (Number.isNaN(date.getTime())) {
    return raw;
  }
  return date.toLocaleString('en-US', {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

function listText(values) {
  const list = Array.isArray(values) ? values.filter(Boolean) : [];
  return list.length > 0 ? list.join(', ') : 'none';
}

function runnerId(runner) {
  return pick(runner, 'runner_id', 'runnerId');
}

function runnerStatus(runner) {
  return String(runner?.status || 'unknown').toLowerCase();
}

function jobId(job) {
  return pick(job, 'job_id', 'jobId');
}

function jobPool(job) {
  return pick(job, 'runner_pool', 'runnerPool') || 'default';
}

function jobImage(job) {
  return String(job?.image || '').trim();
}

function jobStatus(job) {
  return String(job?.status || 'queued').toLowerCase();
}

function buildQueueWarnings(pools, queuedJobs) {
  const poolByName = new Map((pools || []).map((pool) => [String(pool?.name || '').trim(), pool]));
  const warnings = new Map();

  for (const job of queuedJobs || []) {
    const poolName = jobPool(job);
    const pool = poolByName.get(poolName);
    const image = jobImage(job);
    const checkName = job?.check_name || job?.checkName || job?.job_key || job?.jobKey || jobId(job);

    if (!pool) {
      warnings.set(`missing-pool:${poolName}`, {
        key: `missing-pool:${poolName}`,
        title: `Pool ${poolName} is not defined`,
        detail: `${checkName} is queued for a runner pool that is not present in the home CI platform config.`,
      });
      continue;
    }

    if (numberPick(pool, 'online_runners', 'onlineRunners') < 1) {
      warnings.set(`offline:${poolName}`, {
        key: `offline:${poolName}`,
        title: `No online runner in pool ${poolName}`,
        detail: `Queued jobs in this pool will wait until a registered runner starts and heartbeats.`,
      });
    }

    const allowedImages = pool.allowed_images || pool.allowedImages || [];
    if (image && allowedImages.length > 0 && !allowedImages.includes(image)) {
      warnings.set(`image:${poolName}:${image}`, {
        key: `image:${poolName}:${image}`,
        title: `Image ${image} is not allowed in pool ${poolName}`,
        detail: `Update /{home}/.gitslice/ci.yaml or move the job to a compatible pool.`,
      });
    }
  }

  return Array.from(warnings.values());
}

function Metric({ label, value }) {
  return (
    <div className="rounded-md border border-border/70 bg-background/50 p-3">
      <div className="text-xs font-medium uppercase tracking-normal text-muted-foreground">{label}</div>
      <div className="mt-1 font-mono text-xl font-semibold text-foreground">{value}</div>
    </div>
  );
}

function DataError({ children }) {
  if (!children) return null;
  return <div className="panel-error">{children}</div>;
}

export default function CISettingsPanel({
  username,
  settingsRunnerId = '',
  initialSettingsData = null,
}) {
  const hasInitialSettings = initialSettingsData?.username === username;
  const currentKey = `${username || ''}:${settingsRunnerId || ''}`;
  const [loadedKey, setLoadedKey] = useState(() => (hasInitialSettings ? currentKey : ''));
  const [runnerPools, setRunnerPools] = useState(() => (hasInitialSettings ? initialSettingsData.runnerPools || [] : []));
  const [runners, setRunners] = useState(() => (hasInitialSettings ? initialSettingsData.runners || [] : []));
  const [queuedJobs, setQueuedJobs] = useState(() => (hasInitialSettings ? initialSettingsData.queuedJobs || [] : []));
  const [ciRuns, setCIRuns] = useState(() => (hasInitialSettings ? initialSettingsData.ciRuns || [] : []));
  const [selectedRunner, setSelectedRunner] = useState(() => (hasInitialSettings ? initialSettingsData.selectedRunner || null : null));
  const [runnerJobs, setRunnerJobs] = useState(() => (hasInitialSettings ? initialSettingsData.runnerJobs || [] : []));
  const [errors, setErrors] = useState(() => ({
    runnerPools: hasInitialSettings ? initialSettingsData.runnerPoolsError || '' : '',
    runners: hasInitialSettings ? initialSettingsData.runnersError || '' : '',
    queuedJobs: hasInitialSettings ? initialSettingsData.queuedJobsError || '' : '',
    ciRuns: hasInitialSettings ? initialSettingsData.ciRunsError || '' : '',
    selectedRunner: hasInitialSettings ? initialSettingsData.selectedRunnerError || '' : '',
    runnerJobs: hasInitialSettings ? initialSettingsData.runnerJobsError || '' : '',
    token: '',
    action: '',
  }));
  const [loading, setLoading] = useState(() => !hasInitialSettings);
  const [refreshing, setRefreshing] = useState(false);
  const [actionRunnerId, setActionRunnerId] = useState('');
  const [tokenName, setTokenName] = useState('vm-1');
  const [tokenPool, setTokenPool] = useState('default');
  const [tokenLabels, setTokenLabels] = useState('linux,docker');
  const [tokenTTL, setTokenTTL] = useState('30m');
  const [generatedToken, setGeneratedToken] = useState(null);
  const [copyStatus, setCopyStatus] = useState('');
  const [revokeMode, setRevokeMode] = useState('requeue');
  const clientRefreshKeyRef = useRef('');

  const loadData = async ({ quiet = false } = {}) => {
    if (!username) {
      return;
    }
    if (quiet) {
      setRefreshing(true);
    } else {
      setLoading(true);
    }
    setErrors((current) => ({ ...current, runnerPools: '', runners: '', queuedJobs: '', ciRuns: '', selectedRunner: '', runnerJobs: '' }));

    const [poolsResult, runnersResult, queueResult, runsResult] = await Promise.allSettled([
      listRunnerPools(),
      listRunners({ limit: 100 }),
      listQueuedJobs({ limit: 50 }),
      listCIRuns({ limit: 50 }),
    ]);

    const nextErrors = { runnerPools: '', runners: '', queuedJobs: '', ciRuns: '', selectedRunner: '', runnerJobs: '', token: '', action: '' };
    if (poolsResult.status === 'fulfilled') setRunnerPools(poolsResult.value);
    else nextErrors.runnerPools = poolsResult.reason?.message || 'Unable to load runner pools.';

    if (runnersResult.status === 'fulfilled') setRunners(runnersResult.value);
    else nextErrors.runners = runnersResult.reason?.message || 'Unable to load runners.';

    if (queueResult.status === 'fulfilled') setQueuedJobs(queueResult.value);
    else nextErrors.queuedJobs = queueResult.reason?.message || 'Unable to load queued jobs.';

    if (runsResult.status === 'fulfilled') setCIRuns(runsResult.value);
    else nextErrors.ciRuns = runsResult.reason?.message || 'Unable to load CI runs.';

    const runnerID = String(settingsRunnerId || '').trim();
    if (runnerID) {
      const [runnerResult, jobsResult] = await Promise.allSettled([
        getRunner(runnerID),
        listRunnerJobs(runnerID, 30),
      ]);
      if (runnerResult.status === 'fulfilled') setSelectedRunner(runnerResult.value);
      else nextErrors.selectedRunner = runnerResult.reason?.message || 'Unable to load runner details.';

      if (jobsResult.status === 'fulfilled') setRunnerJobs(jobsResult.value);
      else nextErrors.runnerJobs = jobsResult.reason?.message || 'Unable to load runner jobs.';
    } else {
      setSelectedRunner(null);
      setRunnerJobs([]);
    }

    setErrors(nextErrors);
    setLoadedKey(currentKey);
    setLoading(false);
    setRefreshing(false);
  };

  useEffect(() => {
    if (!username) {
      setRunnerPools([]);
      setRunners([]);
      setQueuedJobs([]);
      setCIRuns([]);
      setSelectedRunner(null);
      setRunnerJobs([]);
      setLoadedKey('');
      setLoading(false);
      return;
    }
    if (loadedKey === currentKey && clientRefreshKeyRef.current === currentKey) {
      return;
    }
    clientRefreshKeyRef.current = currentKey;
    loadData({ quiet: loadedKey === currentKey });
  }, [currentKey, loadedKey, username]);

  const totals = useMemo(() => {
    const online = runnerPools.reduce((sum, pool) => sum + numberPick(pool, 'online_runners', 'onlineRunners'), 0);
    const busy = runnerPools.reduce((sum, pool) => sum + numberPick(pool, 'busy_runners', 'busyRunners'), 0);
    return {
      pools: runnerPools.length,
      runners: runners.length,
      online,
      busy,
      queued: queuedJobs.length,
    };
  }, [queuedJobs.length, runnerPools, runners.length]);

  const queueWarnings = useMemo(() => buildQueueWarnings(runnerPools, queuedJobs), [queuedJobs, runnerPools]);
  const tokenCommand = generatedToken?.token
    ? `gs runner register --token ${generatedToken.token}\ngs runner start --executor docker`
    : '';

  async function handleTokenCreate(event) {
    event.preventDefault();
    setGeneratedToken(null);
    setErrors((current) => ({ ...current, token: '' }));
    const labels = tokenLabels.split(',').map((label) => label.trim()).filter(Boolean);
    try {
      const token = await createRunnerToken({
        name: tokenName.trim(),
        pool: tokenPool.trim() || 'default',
        labels,
        ttl: tokenTTL.trim() || '30m',
      });
      setGeneratedToken(token);
      setCopyStatus('');
      await loadData({ quiet: true });
    } catch (error) {
      setErrors((current) => ({ ...current, token: error?.message || 'Unable to create runner token.' }));
    }
  }

  async function handleCopyTokenCommand() {
    if (!tokenCommand) return;
    try {
      await copyToClipboard(tokenCommand);
      setCopyStatus('Copied');
    } catch (error) {
      setCopyStatus(error?.message || 'Copy failed');
    }
  }

  async function runRunnerAction(runnerID, action) {
    setActionRunnerId(runnerID);
    setErrors((current) => ({ ...current, action: '' }));
    try {
      if (action === 'disable') {
        await disableRunner(runnerID, 'Disabled from web settings');
      } else if (action === 'enable') {
        await enableRunner(runnerID);
      } else if (action === 'revoke') {
        await revokeRunner(runnerID, {
          reason: 'Revoked from web settings',
          requeueLeased: revokeMode === 'requeue',
          cancelLeased: revokeMode === 'cancel',
        });
      }
      await loadData({ quiet: true });
    } catch (error) {
      setErrors((current) => ({ ...current, action: error?.message || 'Runner action failed.' }));
    } finally {
      setActionRunnerId('');
    }
  }

  return (
    <div className="space-y-4" data-testid="settings-ci-panel">
      <div className="flex flex-col gap-3 md:flex-row md:items-center md:justify-between">
        <div>
          <h3 className="text-xl font-semibold tracking-normal text-foreground">CI executors</h3>
          <p className="status">Manage user-hosted runners, queue health, and registration tokens for this home.</p>
        </div>
        <Button type="button" variant="outline" onClick={() => loadData({ quiet: true })} disabled={loading || refreshing}>
          <RefreshCw className="h-4 w-4" />
          {refreshing ? 'Refreshing' : 'Refresh'}
        </Button>
      </div>

      {loading && <div className="panel-empty">Loading CI executor state...</div>}
      <DataError>{errors.action}</DataError>

      <div className="grid gap-3 md:grid-cols-5">
        <Metric label="Pools" value={totals.pools} />
        <Metric label="Runners" value={totals.runners} />
        <Metric label="Online" value={totals.online} />
        <Metric label="Busy" value={totals.busy} />
        <Metric label="Queued" value={totals.queued} />
      </div>

      <Card className="border-border/70">
        <CardContent className="space-y-4 pt-6">
          <div className="flex flex-col gap-2 md:flex-row md:items-start md:justify-between">
            <div>
              <h4 className="text-base font-semibold">Registration token</h4>
              <p className="status">Tokens are short-lived and shown once. Register from the VM that will run jobs.</p>
            </div>
            <Badge variant="outline">Docker preferred</Badge>
          </div>
          <form className="grid gap-3 md:grid-cols-[1fr_1fr_1fr_120px_auto]" onSubmit={handleTokenCreate}>
            <label className="space-y-2">
              <span className="text-sm font-medium">Runner name</span>
              <input className="h-10 w-full rounded-md border border-border bg-background px-3 text-sm" value={tokenName} onChange={(event) => setTokenName(event.target.value)} />
            </label>
            <label className="space-y-2">
              <span className="text-sm font-medium">Pool</span>
              <input className="h-10 w-full rounded-md border border-border bg-background px-3 text-sm" value={tokenPool} onChange={(event) => setTokenPool(event.target.value)} />
            </label>
            <label className="space-y-2">
              <span className="text-sm font-medium">Labels</span>
              <input className="h-10 w-full rounded-md border border-border bg-background px-3 text-sm" value={tokenLabels} onChange={(event) => setTokenLabels(event.target.value)} />
            </label>
            <label className="space-y-2">
              <span className="text-sm font-medium">TTL</span>
              <input className="h-10 w-full rounded-md border border-border bg-background px-3 text-sm" value={tokenTTL} onChange={(event) => setTokenTTL(event.target.value)} />
            </label>
            <div className="flex items-end">
              <Button type="submit">Create</Button>
            </div>
          </form>
          <DataError>{errors.token}</DataError>
          {generatedToken?.token && (
            <div className="rounded-md border border-border/70 bg-muted/40 p-3" data-testid="settings-ci-token">
              <div className="mb-2 flex flex-col gap-2 md:flex-row md:items-center md:justify-between">
                <div className="text-sm font-medium">Token expires {formatDateTime(generatedToken.expires_at || generatedToken.expiresAt)}</div>
                <Button type="button" size="sm" variant="outline" onClick={handleCopyTokenCommand}>
                  <Clipboard className="h-4 w-4" />
                  {copyStatus || 'Copy commands'}
                </Button>
              </div>
              <pre className="overflow-auto rounded border border-border/60 bg-background p-3 font-mono text-xs">{tokenCommand}</pre>
            </div>
          )}
        </CardContent>
      </Card>

      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(360px,0.7fr)]">
        <Card className="border-border/70">
          <CardContent className="space-y-4 pt-6">
            <div className="flex items-center justify-between gap-3">
              <div>
                <h4 className="text-base font-semibold">Runner pools</h4>
                <p className="status">Policy comes from <code>/{'{home}'}/.gitslice/ci.yaml</code>.</p>
              </div>
              <Server className="h-5 w-5 text-muted-foreground" />
            </div>
            <DataError>{errors.runnerPools}</DataError>
            {!errors.runnerPools && runnerPools.length === 0 && <div className="panel-empty">No runner pools are configured yet.</div>}
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

        <Card className="border-border/70">
          <CardContent className="space-y-4 pt-6">
            <div className="flex items-center justify-between gap-3">
              <div>
                <h4 className="text-base font-semibold">Queue diagnostics</h4>
                <p className="status">Warnings call out missing compatible capacity before users wait on CI.</p>
              </div>
              <AlertTriangle className="h-5 w-5 text-muted-foreground" />
            </div>
            <DataError>{errors.queuedJobs}</DataError>
            {queueWarnings.length === 0 && !errors.queuedJobs && (
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
                      <div className="mt-1 text-xs text-muted-foreground">{jobImage(job) || 'no image'} · {job.manifest_path || job.manifestPath || 'manifest unknown'}</div>
                    </div>
                  ))}
                </div>
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      <Card className="border-border/70">
        <CardContent className="space-y-4 pt-6">
          <div className="flex flex-col gap-2 md:flex-row md:items-start md:justify-between">
            <div>
              <h4 className="text-base font-semibold">Registered runners</h4>
              <p className="status">Disable stops new leases. Revoke invalidates the runner credential.</p>
            </div>
            <div className="flex items-center gap-2">
              <select className="h-9 rounded-md border border-border bg-background px-2 text-sm" value={revokeMode} onChange={(event) => setRevokeMode(event.target.value)}>
                <option value="requeue">Requeue leased jobs</option>
                <option value="cancel">Cancel leased jobs</option>
                <option value="leave">Leave leased jobs</option>
              </select>
            </div>
          </div>
          <DataError>{errors.runners}</DataError>
          {!errors.runners && runners.length === 0 && <div className="panel-empty">No runners have registered yet.</div>}
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
                          <div className="text-xs text-muted-foreground">{runner.name || 'unnamed'} · {listText(runner.labels)}</div>
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
                              <Button type="button" size="sm" variant="outline" disabled={actionRunnerId === id} onClick={() => runRunnerAction(id, 'enable')}>Enable</Button>
                            ) : (
                              <Button type="button" size="sm" variant="outline" disabled={actionRunnerId === id} onClick={() => runRunnerAction(id, 'disable')}>
                                <Ban className="h-4 w-4" />
                                Disable
                              </Button>
                            )}
                            <Button type="button" size="sm" variant="destructive" disabled={actionRunnerId === id} onClick={() => runRunnerAction(id, 'revoke')}>
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

      {settingsRunnerId && (
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
      )}

      <Card className="border-border/70">
        <CardContent className="space-y-4 pt-6">
          <div>
            <h4 className="text-base font-semibold">Recent CI runs</h4>
            <p className="status">The latest runs show exact changeset versions and plan hashes when available.</p>
          </div>
          <DataError>{errors.ciRuns}</DataError>
          {!errors.ciRuns && ciRuns.length === 0 && <div className="panel-empty">No CI runs yet.</div>}
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
    </div>
  );
}
