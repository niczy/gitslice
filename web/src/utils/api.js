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
  updateCurrentUser,
} from '../api/account.js';

export {
  addSliceFolder,
  createAndMergeChangeset,
  createChangeset,
  createRevertChangeset,
  deleteSliceEnvKV,
  createSliceFromFolder,
  fetchSliceEntries,
  getSliceEnvRequirements,
  getPathVisibility,
  getSliceVisibility,
  listSliceEnvKV,
  listSliceChangesets,
  listSliceCommits,
  removeSliceFolder,
  searchWorkspaceFiles,
  setSliceEnvSecret,
  setSliceEnvValue,
  updatePathVisibility,
  updateSliceVisibility,
} from '../api/slices.js';

export {
  closeChangeset,
  getChangesetArtifactLinks,
  getChangesetDiff,
  getCommitArtifactLinks,
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
