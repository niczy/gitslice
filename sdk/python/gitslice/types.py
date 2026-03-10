from __future__ import annotations

from dataclasses import dataclass
from typing import Any, Mapping


def _as_int(value: Any) -> int:
    if value in (None, ""):
        return 0
    return int(value)


@dataclass(frozen=True, slots=True)
class WorkspaceInfo:
    workspace_id: str
    name: str
    description: str
    created_by: str
    owners: list[str]
    head_commit_hash: str
    created_at: int
    updated_at: int
    path_count: int
    file_count: int
    is_root: bool

    @classmethod
    def from_api(cls, payload: Mapping[str, Any]) -> "WorkspaceInfo":
        return cls(
            workspace_id=str(payload.get("workspaceId", "")),
            name=str(payload.get("name", "")),
            description=str(payload.get("description", "")),
            created_by=str(payload.get("createdBy", "")),
            owners=[str(owner) for owner in payload.get("owners", [])],
            head_commit_hash=str(payload.get("headCommitHash", "")),
            created_at=_as_int(payload.get("createdAt")),
            updated_at=_as_int(payload.get("updatedAt")),
            path_count=_as_int(payload.get("pathCount")),
            file_count=_as_int(payload.get("fileCount")),
            is_root=bool(payload.get("isRoot", False)),
        )


@dataclass(frozen=True, slots=True)
class WorkspaceEntry:
    name: str
    path: str
    entry_type: str
    size: int
    hash: str

    @classmethod
    def from_api(cls, payload: Mapping[str, Any]) -> "WorkspaceEntry":
        return cls(
            name=str(payload.get("name", "")),
            path=str(payload.get("path", "")),
            entry_type=str(payload.get("type", "")),
            size=_as_int(payload.get("size")),
            hash=str(payload.get("hash", "")),
        )


@dataclass(frozen=True, slots=True)
class WriteResult:
    workspace_id: str
    path: str
    size: int
    hash: str
    commit_hash: str

    @classmethod
    def from_api(cls, payload: Mapping[str, Any]) -> "WriteResult":
        return cls(
            workspace_id=str(payload.get("workspaceId", "")),
            path=str(payload.get("path", "")),
            size=_as_int(payload.get("size")),
            hash=str(payload.get("hash", "")),
            commit_hash=str(payload.get("commitHash", "")),
        )


@dataclass(frozen=True, slots=True)
class SearchMatch:
    path: str
    line_number: int
    line: str
    match_start: int
    match_end: int

    @classmethod
    def from_api(cls, payload: Mapping[str, Any]) -> "SearchMatch":
        return cls(
            path=str(payload.get("path", "")),
            line_number=_as_int(payload.get("lineNumber")),
            line=str(payload.get("line", "")),
            match_start=_as_int(payload.get("matchStart")),
            match_end=_as_int(payload.get("matchEnd")),
        )


@dataclass(frozen=True, slots=True)
class SnapshotInfo:
    snapshot_id: str
    parent_snapshot_id: str
    message: str
    created_at: int
    file_count: int

    @classmethod
    def from_api(cls, payload: Mapping[str, Any]) -> "SnapshotInfo":
        return cls(
            snapshot_id=str(payload.get("snapshotId", "")),
            parent_snapshot_id=str(payload.get("parentSnapshotId", "")),
            message=str(payload.get("message", "")),
            created_at=_as_int(payload.get("createdAt")),
            file_count=_as_int(payload.get("fileCount")),
        )


@dataclass(frozen=True, slots=True)
class DiffSummary:
    files_added: int
    files_modified: int
    files_deleted: int
    lines_added: int
    lines_deleted: int

    @classmethod
    def from_api(cls, payload: Mapping[str, Any]) -> "DiffSummary":
        return cls(
            files_added=_as_int(payload.get("filesAdded")),
            files_modified=_as_int(payload.get("filesModified")),
            files_deleted=_as_int(payload.get("filesDeleted")),
            lines_added=_as_int(payload.get("linesAdded")),
            lines_deleted=_as_int(payload.get("linesDeleted")),
        )


@dataclass(frozen=True, slots=True)
class FileDiff:
    path: str
    change_type: str
    old_hash: str
    new_hash: str
    lines_added: int
    lines_deleted: int
    patch: str

    @classmethod
    def from_api(cls, payload: Mapping[str, Any]) -> "FileDiff":
        return cls(
            path=str(payload.get("path", "")),
            change_type=str(payload.get("changeType", "")),
            old_hash=str(payload.get("oldHash", "")),
            new_hash=str(payload.get("newHash", "")),
            lines_added=_as_int(payload.get("linesAdded")),
            lines_deleted=_as_int(payload.get("linesDeleted")),
            patch=str(payload.get("patch", "")),
        )


@dataclass(frozen=True, slots=True)
class DiffResult:
    workspace_id: str
    from_snapshot_id: str
    to_snapshot_id: str
    summary: DiffSummary
    files: list[FileDiff]

    @classmethod
    def from_api(cls, payload: Mapping[str, Any]) -> "DiffResult":
        return cls(
            workspace_id=str(payload.get("workspaceId", "")),
            from_snapshot_id=str(payload.get("fromSnapshotId", "")),
            to_snapshot_id=str(payload.get("toSnapshotId", "")),
            summary=DiffSummary.from_api(payload.get("summary", {})),
            files=[FileDiff.from_api(item) for item in payload.get("files", [])],
        )


@dataclass(frozen=True, slots=True)
class MergeConflict:
    path: str
    base_hash: str
    source_hash: str
    target_hash: str
    source_change_type: str
    target_change_type: str
    source_patch: str
    target_patch: str

    @classmethod
    def from_api(cls, payload: Mapping[str, Any]) -> "MergeConflict":
        return cls(
            path=str(payload.get("path", "")),
            base_hash=str(payload.get("baseHash", "")),
            source_hash=str(payload.get("sourceHash", "")),
            target_hash=str(payload.get("targetHash", "")),
            source_change_type=str(payload.get("sourceChangeType", "")),
            target_change_type=str(payload.get("targetChangeType", "")),
            source_patch=str(payload.get("sourcePatch", "")),
            target_patch=str(payload.get("targetPatch", "")),
        )


@dataclass(frozen=True, slots=True)
class MergeResult:
    workspace_id: str
    source_workspace_id: str
    base_workspace_id: str
    base_snapshot_id: str
    source_snapshot_id: str
    target_snapshot_id: str
    status: str
    commit_hash: str
    merged_paths: list[str]
    summary: DiffSummary
    conflicts: list[MergeConflict]

    @classmethod
    def from_api(cls, payload: Mapping[str, Any]) -> "MergeResult":
        return cls(
            workspace_id=str(payload.get("workspaceId", "")),
            source_workspace_id=str(payload.get("sourceWorkspaceId", "")),
            base_workspace_id=str(payload.get("baseWorkspaceId", "")),
            base_snapshot_id=str(payload.get("baseSnapshotId", "")),
            source_snapshot_id=str(payload.get("sourceSnapshotId", "")),
            target_snapshot_id=str(payload.get("targetSnapshotId", "")),
            status=str(payload.get("status", "")),
            commit_hash=str(payload.get("commitHash", "")),
            merged_paths=[str(path) for path in payload.get("mergedPaths", [])],
            summary=DiffSummary.from_api(payload.get("summary", {})),
            conflicts=[MergeConflict.from_api(item) for item in payload.get("conflicts", [])],
        )


@dataclass(frozen=True, slots=True)
class WorkspaceConflict:
    path: str
    workspace_ids: list[str]

    @classmethod
    def from_api(cls, payload: Mapping[str, Any]) -> "WorkspaceConflict":
        return cls(
            path=str(payload.get("path", "")),
            workspace_ids=[str(item) for item in payload.get("workspaceIds", [])],
        )
