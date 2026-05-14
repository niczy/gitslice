export function pick(value, snakeName, camelName, fallback = '') {
  return value?.[snakeName] ?? value?.[camelName] ?? fallback;
}

export function numberPick(value, snakeName, camelName, fallback = 0) {
  const next = Number(pick(value, snakeName, camelName, fallback));
  return Number.isFinite(next) ? next : fallback;
}

export function statusVariant(status) {
  const normalized = String(status || '').toLowerCase();
  if (['idle', 'online', 'passed', 'success', 'active'].includes(normalized)) {
    return 'secondary';
  }
  if (['failed', 'error', 'disabled', 'revoked', 'cancelled'].includes(normalized)) {
    return 'destructive';
  }
  return 'outline';
}

export function formatDateTime(value) {
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

export function listText(values) {
  const list = Array.isArray(values) ? values.filter(Boolean) : [];
  return list.length > 0 ? list.join(', ') : 'none';
}

export function runnerId(runner) {
  return pick(runner, 'runner_id', 'runnerId');
}

export function runnerStatus(runner) {
  return String(runner?.status || 'unknown').toLowerCase();
}

export function jobId(job) {
  return pick(job, 'job_id', 'jobId');
}

export function jobPool(job) {
  return pick(job, 'runner_pool', 'runnerPool') || 'default';
}

export function jobImage(job) {
  return String(job?.image || '').trim();
}

export function jobStatus(job) {
  return String(job?.status || 'queued').toLowerCase();
}

export function buildQueueWarnings(pools, queuedJobs) {
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
        detail: 'Queued jobs in this pool will wait until a registered runner starts and heartbeats.',
      });
    }

    const allowedImages = pool.allowed_images || pool.allowedImages || [];
    if (image && allowedImages.length > 0 && !allowedImages.includes(image)) {
      warnings.set(`image:${poolName}:${image}`, {
        key: `image:${poolName}:${image}`,
        title: `Image ${image} is not allowed in pool ${poolName}`,
        detail: 'Update /{home}/.gitslice/ci.yaml or move the job to a compatible pool.',
      });
    }
  }

  return Array.from(warnings.values());
}
