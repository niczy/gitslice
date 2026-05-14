// ---------------------------------------------------------------------------
// API compatibility barrel
// ---------------------------------------------------------------------------

export {
  apiBaseUrl,
  currentUsername,
  fetchWithAuth,
  readErrorMessage,
} from '../api/client.js';

export {
  appendAgentSessionEvent,
  createAgentKey,
  createAgentSession,
  fetchAgentKeys,
  getAgentCapabilities,
  listAgentRunners,
  listAgentSessionEvents,
  listAgentSessions,
  mintAgentSessionToken,
  requestAgentRunnerRestart,
  requestAgentSessionChangesetExport,
  requestAgentSessionLocalChanges,
  revokeAgentKey,
  sendAgentSessionInput,
} from '../api/agents.js';

export {
  deleteAdminUserByEmail,
  deleteAuthMethod,
  deleteAuthSession,
  deleteCurrentUser,
  fetchAdminStatus,
  fetchAuthContext,
  fetchAuthMethods,
  fetchAuthSessions,
  fetchCurrentUser,
  fetchRepoBindings,
  updateCurrentUser,
} from '../api/account.js';

export {
  addSliceFolder,
  createRevertChangeset,
  createSliceFromFolder,
  fetchSliceEntries,
  getPathVisibility,
  getSliceVisibility,
  listSliceChangesets,
  listSliceCommits,
  removeSliceFolder,
  searchWorkspaceFiles,
  updatePathVisibility,
  updateSliceVisibility,
} from '../api/slices.js';

export {
  closeChangeset,
  getChangesetDiff,
  listChangesetSnapshots,
  mergeChangeset,
} from '../api/changesets.js';

export {
  cancelCIRun,
  createRunnerToken,
  disableRunner,
  enableRunner,
  getRunner,
  listChangesetChecks,
  listCIRuns,
  listQueuedJobs,
  listRunnerJobs,
  listRunnerPools,
  listRunners,
  rerunCI,
  revokeRunner,
} from '../api/ci.js';
