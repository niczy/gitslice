export {
  AuthenticationError,
  ConflictError,
  GitsliceClient,
  GitsliceError,
  NotFoundError,
  PermissionDeniedError,
  ServerError,
  ValidationError,
  type FetchLike,
  type GitsliceClientOptions,
  type WorkspaceOptions,
} from "./client.js";
export { HomeFilesystem } from "./home.js";
export { Workspace, type ForkOptions, type MergeOptions } from "./workspace.js";
export type {
  DiffResult,
  DiffSummary,
  FileDiff,
  MergeConflict,
  MergeResult,
  SearchMatch,
  SnapshotInfo,
  WorkspaceConflict,
  WorkspaceEntry,
  WorkspaceInfo,
  WriteResult,
} from "./types.js";
