import { getConfiguredAPIBaseURL } from '../shared/runtime.js';
import { clearLocalSessionCookie, getAuthProvider, getProxyAuthorizationResult } from './auth.js';
import {
  normalizeChangesetDiffResponse,
  normalizeChangesetListResponse,
  normalizeChangesetSnapshotListResponse,
  normalizeCommitListResponse,
  normalizeDiffResponse,
  normalizeSliceInfo,
} from '../src/utils/normalize.js';

const SLICE_LIST_LIMIT = 200;
const COMMIT_PAGE_SIZE = 100;
const CHANGESET_PAGE_SIZE = 100;

function getGatewayTarget() {
  return getConfiguredAPIBaseURL(process.env, 'http://localhost:50051');
}

function encodePath(value) {
  return String(value || '').split('/').filter(Boolean).map(encodeURIComponent).join('/');
}

function buildGatewayURL(pathname, request, params = new URLSearchParams()) {
  const target = new URL(pathname, getGatewayTarget());
  const sourceURL = new URL(request.url);
  for (const [key, value] of params.entries()) {
    target.searchParams.set(key, value);
  }
  if (!target.search && sourceURL.search && pathname.startsWith('/v1/slices')) {
    // Browser file/detail requests may carry sliceHash in the current route.
    const sliceHash = sourceURL.searchParams.get('sliceHash');
    if (sliceHash) {
      target.searchParams.set('slice_version.slice_hash', sliceHash);
    }
  }
  return target;
}

async function createGatewayHeaders(request, session, options = {}) {
  const headers = new Headers({
    Accept: 'application/json',
  });

  const requestAuthorization = request.headers.get('Authorization');
  if (requestAuthorization) {
    headers.set('Authorization', requestAuthorization);
    if (!(getAuthProvider() === 'clerk' && /^Bearer\s+/i.test(String(requestAuthorization || '').trim()))) {
      return { headers, setCookies: [] };
    }
  }

  if (getAuthProvider() === 'clerk') {
    const authResult = await getProxyAuthorizationResult(request, options);
    if (authResult.authorization) {
      headers.set('Authorization', authResult.authorization);
    } else {
      headers.delete('Authorization');
    }
    return {
      headers,
      setCookies: authResult.setCookies || [],
      rejectUnauthenticated: authResult.rejectUnauthenticated || /^Bearer\s+/i.test(String(requestAuthorization || '').trim()),
    };
  }

  const username = String(session?.user?.username || '').trim();
  if (username) {
    headers.set('Authorization', `User ${username}`);
  }

  return { headers, setCookies: [] };
}

async function readErrorMessage(response, fallback) {
  let detail = '';
  try {
    const text = await response.text();
    if (text) {
      try {
        const payload = JSON.parse(text);
        detail = payload?.message || payload?.error || '';
      } catch {
        detail = text;
      }
    }
  } catch {
    detail = '';
  }
  return detail ? `${fallback}: ${detail}` : `${fallback} (${response.status})`;
}

async function fetchJSON(request, session, pathname, params, options = {}) {
  const { headers, setCookies, rejectUnauthenticated } = await createGatewayHeaders(request, session, options);
  if (rejectUnauthenticated) {
    const error = new Error('Request failed: Not signed in');
    error.setCookies = setCookies;
    error.authExpired = true;
    error.status = 401;
    throw error;
  }
  const response = await fetch(buildGatewayURL(pathname, request, params), {
    headers,
  });
  if (!response.ok) {
    const message = await readErrorMessage(response, 'Request failed');
    const error = new Error(message);
    error.setCookies = setCookies;
    error.status = response.status;
    if (response.status === 401 && /invalid session token/i.test(message)) {
      error.setCookies = [...setCookies, clearLocalSessionCookie(request)];
      error.authExpired = true;
    }
    throw error;
  }
  return {
    payload: await response.json(),
    setCookies,
  };
}

function pushErrorCookies(target, error) {
  if (Array.isArray(error?.setCookies)) {
    target.push(...error.setCookies);
  }
}

function recordRouteError(data, setCookies, error) {
  pushErrorCookies(setCookies, error);
  data.authExpired = data.authExpired || Boolean(error?.authExpired);
}

function shouldTreatBrowserRouteAsNotFound(routeInfo, sliceId, error) {
  const requestedSlice = String(routeInfo?.browserState?.slice || '').trim();
  if (routeInfo?.page !== 'browser' || !requestedSlice || sliceId === 'root') {
    return false;
  }
  return [401, 403, 404].includes(Number(error?.status));
}

function changesetStatusQueryValue(statusFilter) {
  switch (statusFilter) {
    case 'pending':
      return '0';
    case 'approved':
      return '1';
    case 'rejected':
      return '2';
    case 'merged':
      return '3';
    default:
      return '';
  }
}

function createRouteData(routeInfo) {
  return {
    slices: null,
    slicesError: '',
    selectedSliceId: routeInfo?.browserState?.slice || '',
    sliceHash: routeInfo?.browserState?.sliceHash || '',
    rootEntries: null,
    rootEntriesError: '',
    selectedFile: routeInfo?.browserState?.file || '',
    selectedFilePayload: null,
    selectedFileError: '',
    sliceCommitsSliceId: routeInfo?.page === 'slice-commits' ? routeInfo?.browserState?.slice || '' : '',
    sliceCommits: null,
    sliceCommitsError: '',
    sliceCommitsHasMore: false,
    sliceChangesetsSliceId: routeInfo?.page === 'slice-changesets' ? routeInfo?.browserState?.slice || '' : '',
    sliceChangesetsStatusFilter: 'all',
    sliceChangesets: null,
    sliceChangesetsError: '',
    commitDiffHash: routeInfo?.page === 'diff' ? routeInfo?.commitHash || '' : '',
    commitDiff: null,
    commitDiffError: '',
    changesetId: routeInfo?.page === 'changeset' ? routeInfo?.changesetId || '' : '',
    changesetSnapshots: null,
    changesetSnapshotsError: '',
    changesetSnapshotVersion: 0,
    changesetDiff: null,
    changesetDiffError: '',
    settingsSection: routeInfo?.settingsSection || '',
    settingsRunnerId: routeInfo?.settingsRunnerId || '',
    settings: null,
  };
}

function pageNeedsSlices(page) {
  return ['browser', 'projects', 'slice-commits', 'slice-changesets', 'slice-agents'].includes(page);
}

function routeNeedsSlices(routeInfo, session) {
  return pageNeedsSlices(routeInfo?.page) || (routeInfo?.page === 'landing' && Boolean(session?.user?.username));
}

async function loadSlices(request, session, data, setCookies, options = {}) {
  const { payload, setCookies: cookies } = await fetchJSON(
    request,
    session,
    '/v1/slices',
    new URLSearchParams({ limit: String(SLICE_LIST_LIMIT) }),
    options,
  );
  setCookies.push(...cookies);
  data.slices = (payload?.slices || []).map(normalizeSliceInfo);
}

async function loadBrowserData(request, session, routeInfo, data, setCookies, options = {}) {
  if (routeInfo?.page !== 'browser' || !data.selectedSliceId) {
    return;
  }

  try {
    const params = new URLSearchParams();
    if (routeInfo.browserState?.sliceHash) {
      params.set('slice_version.slice_hash', routeInfo.browserState.sliceHash);
    }
    const { payload, setCookies: cookies } = await fetchJSON(
      request,
      session,
      `/v1/slices/${encodeURIComponent(data.selectedSliceId)}/entries`,
      params,
      options,
    );
    setCookies.push(...cookies);
    data.rootEntries = payload?.entries || [];
  } catch (error) {
    recordRouteError(data, setCookies, error);
    data.rootEntriesError = error?.message || 'Unable to load entries.';
    data.routeNotFound = data.routeNotFound || shouldTreatBrowserRouteAsNotFound(routeInfo, data.selectedSliceId, error);
  }

  if (!data.selectedFile) {
    return;
  }

  try {
    const params = new URLSearchParams();
    if (routeInfo.browserState?.sliceHash) {
      params.set('slice_version.slice_hash', routeInfo.browserState.sliceHash);
    }
    const encodedFile = encodePath(data.selectedFile);
    const { payload, setCookies: cookies } = await fetchJSON(
      request,
      session,
      `/v1/slices/${encodeURIComponent(data.selectedSliceId)}/files/${encodedFile}`,
      params,
      options,
    );
    setCookies.push(...cookies);
    data.selectedFilePayload = payload?.file || null;
  } catch (error) {
    recordRouteError(data, setCookies, error);
    data.selectedFileError = error?.message || 'Unable to load file content.';
  }
}

async function loadSliceCommits(request, session, data, setCookies, options = {}) {
  if (!data.sliceCommitsSliceId) {
    return;
  }

  try {
    const { payload, setCookies: cookies } = await fetchJSON(
      request,
      session,
      `/v1/slices/${encodeURIComponent(data.sliceCommitsSliceId)}/commits`,
      new URLSearchParams({ limit: String(COMMIT_PAGE_SIZE) }),
      options,
    );
    setCookies.push(...cookies);
    data.sliceCommits = normalizeCommitListResponse(payload);
    data.sliceCommitsHasMore = data.sliceCommits.length === COMMIT_PAGE_SIZE;
  } catch (error) {
    recordRouteError(data, setCookies, error);
    data.sliceCommits = null;
    data.sliceCommitsError = error?.message || 'Unable to load commits.';
  }
}

async function loadSliceChangesets(request, session, data, setCookies, options = {}) {
  if (!data.sliceChangesetsSliceId) {
    return;
  }

  const params = new URLSearchParams({ limit: String(CHANGESET_PAGE_SIZE) });
  const statusValue = changesetStatusQueryValue(data.sliceChangesetsStatusFilter);
  if (statusValue) {
    params.set('status_filter', statusValue);
  } else {
    params.set('include_all_statuses', 'true');
  }
  params.set('omit_modified_files', 'true');

  try {
    const { payload, setCookies: cookies } = await fetchJSON(
      request,
      session,
      `/v1/slices/${encodeURIComponent(data.sliceChangesetsSliceId)}/changesets`,
      params,
      options,
    );
    setCookies.push(...cookies);
    data.sliceChangesets = normalizeChangesetListResponse(payload);
  } catch (error) {
    recordRouteError(data, setCookies, error);
    data.sliceChangesets = null;
    data.sliceChangesetsError = error?.message || 'Unable to load changesets.';
  }
}

async function loadCommitDiff(request, session, data, setCookies, options = {}) {
  if (!data.commitDiffHash) {
    return;
  }

  try {
    const { payload, setCookies: cookies } = await fetchJSON(
      request,
      session,
      `/v1/commits/${encodeURIComponent(data.commitDiffHash)}/changes`,
      undefined,
      options,
    );
    setCookies.push(...cookies);
    data.commitDiff = normalizeDiffResponse(payload);
  } catch (error) {
    recordRouteError(data, setCookies, error);
    data.commitDiffError = error?.message || 'Unable to load commit changes.';
  }
}

async function loadChangesetDiff(request, session, data, setCookies, options = {}) {
  if (!data.changesetId) {
    return;
  }

  try {
    const { payload, setCookies: cookies } = await fetchJSON(
      request,
      session,
      `/v1/changesets/${encodeURIComponent(data.changesetId)}/snapshots`,
      new URLSearchParams({ limit: '100', omit_modified_files: 'true' }),
      options,
    );
    setCookies.push(...cookies);
    data.changesetSnapshots = normalizeChangesetSnapshotListResponse(payload);
    data.changesetSnapshotVersion = data.changesetSnapshots[0]?.version || 0;
  } catch (error) {
    recordRouteError(data, setCookies, error);
    data.changesetSnapshots = null;
    data.changesetSnapshotsError = error?.message || 'Unable to load snapshot versions.';
  }

  try {
    const params = new URLSearchParams();
    if (data.changesetSnapshotVersion > 0) {
      params.set('snapshot_version', String(data.changesetSnapshotVersion));
    }
    const { payload, setCookies: cookies } = await fetchJSON(
      request,
      session,
      `/v1/changesets/${encodeURIComponent(data.changesetId)}/diff`,
      params,
      options,
    );
    setCookies.push(...cookies);
    data.changesetDiff = normalizeChangesetDiffResponse(payload);
  } catch (error) {
    recordRouteError(data, setCookies, error);
    data.changesetDiffError = error?.message || 'Unable to load changeset diff.';
  }
}

async function loadSettingsData(request, session, data, setCookies, options = {}) {
  const username = String(session?.user?.username || '').trim();
  if (!username) {
    return;
  }

  const settings = {
    username,
    bindings: [],
    bindingsError: '',
    authMethods: [],
    authMethodsError: '',
    authContext: null,
    authContextError: '',
    sessions: [],
    sessionsError: '',
    agentKeys: [],
    agentKeysError: '',
    runnerPools: [],
    runnerPoolsError: '',
    runners: [],
    runnersError: '',
    queuedJobs: [],
    queuedJobsError: '',
    ciRuns: [],
    ciRunsError: '',
    selectedRunner: null,
    selectedRunnerError: '',
    runnerJobs: [],
    runnerJobsError: '',
  };
  data.settings = settings;

  const loadSection = async (field, errorField, pathname, transform, params) => {
    try {
      const { payload, setCookies: cookies } = await fetchJSON(request, session, pathname, params, options);
      setCookies.push(...cookies);
      settings[field] = transform(payload);
    } catch (error) {
      recordRouteError(data, setCookies, error);
      settings[errorField] = error?.message || 'Unable to load settings data.';
    }
  };

  await Promise.all([
    loadSection('bindings', 'bindingsError', '/v1/repos/bindings', (payload) => payload?.bindings || []),
    loadSection('authMethods', 'authMethodsError', '/v1/auth/methods', (payload) => payload?.methods || []),
    loadSection('authContext', 'authContextError', '/v1/auth/context', (payload) => payload),
    loadSection('sessions', 'sessionsError', '/v1/auth/sessions', (payload) => payload?.sessions || []),
    loadSection('agentKeys', 'agentKeysError', '/v1/auth/agent/keys', (payload) => payload?.keys || []),
  ]);

  if (!String(data.settingsSection || '').startsWith('ci')) {
    return;
  }

  await Promise.all([
    loadSection('runnerPools', 'runnerPoolsError', '/v1/ci/runner-pools', (payload) => payload?.pools || []),
    loadSection('runners', 'runnersError', '/v1/ci/runners', (payload) => payload?.runners || [], new URLSearchParams({ limit: '100' })),
    loadSection('queuedJobs', 'queuedJobsError', '/v1/ci/queued-jobs', (payload) => payload?.jobs || [], new URLSearchParams({ limit: '50' })),
    loadSection('ciRuns', 'ciRunsError', '/v1/ci/runs', (payload) => payload?.runs || [], new URLSearchParams({ limit: '50' })),
  ]);

  const runnerId = String(data.settingsRunnerId || '').trim();
  if (!runnerId) {
    return;
  }

  await Promise.all([
    loadSection('selectedRunner', 'selectedRunnerError', `/v1/ci/runners/${encodeURIComponent(runnerId)}`, (payload) => payload),
    loadSection('runnerJobs', 'runnerJobsError', `/v1/ci/runners/${encodeURIComponent(runnerId)}/jobs`, (payload) => payload?.jobs || [], new URLSearchParams({ limit: '30' })),
  ]);
}

export async function loadBrowserRouteData(request, session, routeInfo, options = {}) {
  const data = createRouteData(routeInfo);
  const setCookies = [];
  let authExpired = false;

  if (routeNeedsSlices(routeInfo, session)) {
    try {
      await loadSlices(request, session, data, setCookies, options);
    } catch (error) {
      recordRouteError(data, setCookies, error);
      data.slicesError = error?.message || 'Unable to load slices.';
    }
  }

  const markAuthExpired = (error) => {
    authExpired = authExpired || Boolean(error?.authExpired);
  };

  try {
    await loadBrowserData(request, session, routeInfo, data, setCookies, options);
  } catch (error) {
    markAuthExpired(error);
  }

  try {
    await loadSliceCommits(request, session, data, setCookies, options);
  } catch (error) {
    markAuthExpired(error);
  }

  try {
    await loadSliceChangesets(request, session, data, setCookies, options);
  } catch (error) {
    markAuthExpired(error);
  }

  try {
    await loadCommitDiff(request, session, data, setCookies, options);
  } catch (error) {
    markAuthExpired(error);
  }

  try {
    await loadChangesetDiff(request, session, data, setCookies, options);
  } catch (error) {
    markAuthExpired(error);
  }

  try {
    if (routeInfo?.page === 'settings') {
      await loadSettingsData(request, session, data, setCookies, options);
    }
  } catch (error) {
    markAuthExpired(error);
  }

  authExpired = authExpired || Boolean(data.authExpired);
  delete data.authExpired;
  return { data, setCookies, authExpired };
}
