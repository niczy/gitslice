export interface WorkspaceInfo {
  workspaceId: string;
  name: string;
  description: string;
  createdBy: string;
  owners: string[];
  headCommitHash: string;
  createdAt: number;
  updatedAt: number;
  pathCount: number;
  fileCount: number;
  isRoot: boolean;
}

export interface WorkspaceEntry {
  name: string;
  path: string;
  type: string;
  size: number;
  hash: string;
}

export interface WriteResult {
  workspaceId: string;
  path: string;
  size: number;
  hash: string;
  commitHash: string;
}

export interface SearchMatch {
  path: string;
  lineNumber: number;
  line: string;
  matchStart: number;
  matchEnd: number;
}

export interface SnapshotInfo {
  snapshotId: string;
  parentSnapshotId: string;
  message: string;
  createdAt: number;
  fileCount: number;
}

export interface DiffSummary {
  filesAdded: number;
  filesModified: number;
  filesDeleted: number;
  linesAdded: number;
  linesDeleted: number;
}

export interface FileDiff {
  path: string;
  changeType: string;
  oldHash: string;
  newHash: string;
  linesAdded: number;
  linesDeleted: number;
  patch: string;
}

export interface DiffResult {
  workspaceId: string;
  fromSnapshotId: string;
  toSnapshotId: string;
  summary: DiffSummary;
  files: FileDiff[];
}

export interface MergeConflict {
  path: string;
  baseHash: string;
  sourceHash: string;
  targetHash: string;
  sourceChangeType: string;
  targetChangeType: string;
  sourcePatch: string;
  targetPatch: string;
}

export interface MergeResult {
  workspaceId: string;
  sourceWorkspaceId: string;
  baseWorkspaceId: string;
  baseSnapshotId: string;
  sourceSnapshotId: string;
  targetSnapshotId: string;
  status: string;
  commitHash: string;
  mergedPaths: string[];
  summary: DiffSummary;
  conflicts: MergeConflict[];
}

export interface WorkspaceConflict {
  path: string;
  workspaceIds: string[];
}

type ApiRecord = Record<string, unknown>;

function asInt(value: unknown): number {
  if (value === null || value === undefined || value === "") {
    return 0;
  }
  return Number(value);
}

function asStringArray(value: unknown): string[] {
  return Array.isArray(value) ? value.map((item) => String(item)) : [];
}

export function parseWorkspaceInfo(payload: ApiRecord): WorkspaceInfo {
  return {
    workspaceId: String(payload.workspaceId ?? ""),
    name: String(payload.name ?? ""),
    description: String(payload.description ?? ""),
    createdBy: String(payload.createdBy ?? ""),
    owners: asStringArray(payload.owners),
    headCommitHash: String(payload.headCommitHash ?? ""),
    createdAt: asInt(payload.createdAt),
    updatedAt: asInt(payload.updatedAt),
    pathCount: asInt(payload.pathCount),
    fileCount: asInt(payload.fileCount),
    isRoot: Boolean(payload.isRoot ?? false),
  };
}

export function parseWorkspaceEntry(payload: ApiRecord): WorkspaceEntry {
  return {
    name: String(payload.name ?? ""),
    path: String(payload.path ?? ""),
    type: String(payload.type ?? ""),
    size: asInt(payload.size),
    hash: String(payload.hash ?? ""),
  };
}

export function parseWriteResult(payload: ApiRecord): WriteResult {
  return {
    workspaceId: String(payload.workspaceId ?? ""),
    path: String(payload.path ?? ""),
    size: asInt(payload.size),
    hash: String(payload.hash ?? ""),
    commitHash: String(payload.commitHash ?? ""),
  };
}

export function parseSearchMatch(payload: ApiRecord): SearchMatch {
  return {
    path: String(payload.path ?? ""),
    lineNumber: asInt(payload.lineNumber),
    line: String(payload.line ?? ""),
    matchStart: asInt(payload.matchStart),
    matchEnd: asInt(payload.matchEnd),
  };
}

export function parseSnapshotInfo(payload: ApiRecord): SnapshotInfo {
  return {
    snapshotId: String(payload.snapshotId ?? ""),
    parentSnapshotId: String(payload.parentSnapshotId ?? ""),
    message: String(payload.message ?? ""),
    createdAt: asInt(payload.createdAt),
    fileCount: asInt(payload.fileCount),
  };
}

export function parseDiffSummary(payload: ApiRecord): DiffSummary {
  return {
    filesAdded: asInt(payload.filesAdded),
    filesModified: asInt(payload.filesModified),
    filesDeleted: asInt(payload.filesDeleted),
    linesAdded: asInt(payload.linesAdded),
    linesDeleted: asInt(payload.linesDeleted),
  };
}

export function parseFileDiff(payload: ApiRecord): FileDiff {
  return {
    path: String(payload.path ?? ""),
    changeType: String(payload.changeType ?? ""),
    oldHash: String(payload.oldHash ?? ""),
    newHash: String(payload.newHash ?? ""),
    linesAdded: asInt(payload.linesAdded),
    linesDeleted: asInt(payload.linesDeleted),
    patch: String(payload.patch ?? ""),
  };
}

export function parseDiffResult(payload: ApiRecord): DiffResult {
  const files = Array.isArray(payload.files) ? payload.files : [];
  return {
    workspaceId: String(payload.workspaceId ?? ""),
    fromSnapshotId: String(payload.fromSnapshotId ?? ""),
    toSnapshotId: String(payload.toSnapshotId ?? ""),
    summary: parseDiffSummary((payload.summary as ApiRecord | undefined) ?? {}),
    files: files.map((item) => parseFileDiff((item as ApiRecord) ?? {})),
  };
}

export function parseMergeConflict(payload: ApiRecord): MergeConflict {
  return {
    path: String(payload.path ?? ""),
    baseHash: String(payload.baseHash ?? ""),
    sourceHash: String(payload.sourceHash ?? ""),
    targetHash: String(payload.targetHash ?? ""),
    sourceChangeType: String(payload.sourceChangeType ?? ""),
    targetChangeType: String(payload.targetChangeType ?? ""),
    sourcePatch: String(payload.sourcePatch ?? ""),
    targetPatch: String(payload.targetPatch ?? ""),
  };
}

export function parseMergeResult(payload: ApiRecord): MergeResult {
  const mergedPaths = Array.isArray(payload.mergedPaths) ? payload.mergedPaths : [];
  const conflicts = Array.isArray(payload.conflicts) ? payload.conflicts : [];
  return {
    workspaceId: String(payload.workspaceId ?? ""),
    sourceWorkspaceId: String(payload.sourceWorkspaceId ?? ""),
    baseWorkspaceId: String(payload.baseWorkspaceId ?? ""),
    baseSnapshotId: String(payload.baseSnapshotId ?? ""),
    sourceSnapshotId: String(payload.sourceSnapshotId ?? ""),
    targetSnapshotId: String(payload.targetSnapshotId ?? ""),
    status: String(payload.status ?? ""),
    commitHash: String(payload.commitHash ?? ""),
    mergedPaths: mergedPaths.map((item) => String(item)),
    summary: parseDiffSummary((payload.summary as ApiRecord | undefined) ?? {}),
    conflicts: conflicts.map((item) => parseMergeConflict((item as ApiRecord) ?? {})),
  };
}

export function parseWorkspaceConflict(payload: ApiRecord): WorkspaceConflict {
  return {
    path: String(payload.path ?? ""),
    workspaceIds: asStringArray(payload.workspaceIds),
  };
}
