import { useEffect, useMemo, useRef, useState } from 'react';
import { RefreshCw } from 'lucide-react';

import { Button } from './ui/button.jsx';
import CIQueueDiagnosticsCard from './settings/CIQueueDiagnosticsCard.jsx';
import CIRecentRunsCard from './settings/CIRecentRunsCard.jsx';
import CIRegisteredRunnersCard from './settings/CIRegisteredRunnersCard.jsx';
import CIRegistrationTokenCard from './settings/CIRegistrationTokenCard.jsx';
import CIRunnerDetailCard from './settings/CIRunnerDetailCard.jsx';
import CIRunnerPoolsCard from './settings/CIRunnerPoolsCard.jsx';
import { buildQueueWarnings, numberPick } from './settings/CISettingsHelpers.js';
import { DataError, Metric } from './settings/CISettingsPrimitives.jsx';
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

      <CIRegistrationTokenCard
        copyStatus={copyStatus}
        errors={errors}
        generatedToken={generatedToken}
        onCopyTokenCommand={handleCopyTokenCommand}
        onTokenCreate={handleTokenCreate}
        onTokenLabelsChange={setTokenLabels}
        onTokenNameChange={setTokenName}
        onTokenPoolChange={setTokenPool}
        onTokenTTLChange={setTokenTTL}
        tokenCommand={tokenCommand}
        tokenLabels={tokenLabels}
        tokenName={tokenName}
        tokenPool={tokenPool}
        tokenTTL={tokenTTL}
      />

      <div className="grid gap-4 lg:grid-cols-[minmax(0,1fr)_minmax(360px,0.7fr)]">
        <CIRunnerPoolsCard error={errors.runnerPools} runnerPools={runnerPools} />
        <CIQueueDiagnosticsCard error={errors.queuedJobs} queuedJobs={queuedJobs} queueWarnings={queueWarnings} />
      </div>

      <CIRegisteredRunnersCard
        actionRunnerId={actionRunnerId}
        error={errors.runners}
        onRevokeModeChange={setRevokeMode}
        onRunnerAction={runRunnerAction}
        revokeMode={revokeMode}
        runners={runners}
      />

      <CIRunnerDetailCard
        errors={errors}
        runnerJobs={runnerJobs}
        selectedRunner={selectedRunner}
        settingsRunnerId={settingsRunnerId}
      />

      <CIRecentRunsCard ciRuns={ciRuns} error={errors.ciRuns} />
    </div>
  );
}
