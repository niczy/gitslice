from __future__ import annotations

import base64
from typing import Any, Optional
from urllib import parse

from .types import (
    DiffResult,
    MergeResult,
    SearchMatch,
    SnapshotInfo,
    WorkspaceConflict,
    WorkspaceEntry,
    WriteResult,
)


class Workspace:
    def __init__(self, client: Any, workspace_id: str, *, cleanup_on_exit: bool = False) -> None:
        self._client = client
        self.workspace_id = workspace_id
        self._cleanup_on_exit = cleanup_on_exit

    def __enter__(self) -> "Workspace":
        return self

    def __exit__(self, exc_type: Any, exc: Any, tb: Any) -> bool:
        if not self._cleanup_on_exit:
            return False
        try:
            self.delete()
        except Exception:
            if exc is None:
                raise
        return False

    def read(self, path: str, *, encoding: str = "utf-8") -> str:
        return self.read_bytes(path).decode(encoding)

    def read_bytes(self, path: str) -> bytes:
        payload = self._client.request_json("GET", self._file_path(path))
        return self._decode_bytes(payload["content"])

    def write(self, path: str, content: str | bytes, *, encoding: str = "utf-8") -> WriteResult:
        payload = self._client.request_json(
            "PUT",
            self._file_path(path),
            payload={
                "workspaceId": self.workspace_id,
                "path": path,
                "content": self._encode_bytes(content, encoding=encoding),
            },
        )
        return WriteResult.from_api(payload)

    def delete(self) -> dict[str, Any]:
        return self._client.delete_workspace(self.workspace_id)

    def rm(self, path: str) -> dict[str, Any]:
        return self._client.request_json("DELETE", self._file_path(path))

    def mkdir(self, path: str) -> dict[str, Any]:
        escaped = parse.quote(path.strip("/"), safe="/")
        return self._client.request_json(
            "POST",
            f"/v1/fs/workspaces/{parse.quote(self.workspace_id, safe='')}/mkdir/{escaped}",
            payload={"workspaceId": self.workspace_id, "path": path},
        )

    def ls(self, path: str = "") -> list[WorkspaceEntry]:
        request_path = f"/v1/fs/workspaces/{parse.quote(self.workspace_id, safe='')}/ls"
        if path:
            request_path += f"/{parse.quote(path.strip('/'), safe='/')}"
        payload = self._client.request_json("GET", request_path)
        return [WorkspaceEntry.from_api(item) for item in payload.get("entries", [])]

    def exists(self, path: str) -> bool:
        payload = self._client.request_json("GET", self._path_route("exists", path))
        return bool(payload.get("exists", False))

    def stat(self, path: str) -> Optional[WorkspaceEntry]:
        payload = self._client.request_json("GET", self._path_route("stat", path))
        if not payload.get("exists", False):
            return None
        return WorkspaceEntry.from_api(payload.get("entry", {}))

    def mv(self, source_path: str, destination_path: str) -> dict[str, Any]:
        return self._client.request_json(
            "POST",
            f"/v1/fs/workspaces/{parse.quote(self.workspace_id, safe='')}:move",
            payload={
                "workspaceId": self.workspace_id,
                "sourcePath": source_path,
                "destinationPath": destination_path,
            },
        )

    def cp(self, source_path: str, destination_path: str) -> dict[str, Any]:
        return self._client.request_json(
            "POST",
            f"/v1/fs/workspaces/{parse.quote(self.workspace_id, safe='')}:copy",
            payload={
                "workspaceId": self.workspace_id,
                "sourcePath": source_path,
                "destinationPath": destination_path,
            },
        )

    def read_many(self, paths: list[str], *, encoding: str = "utf-8") -> dict[str, Optional[str]]:
        payload = self._client.request_json(
            "POST",
            f"/v1/fs/workspaces/{parse.quote(self.workspace_id, safe='')}:readFiles",
            payload={"workspaceId": self.workspace_id, "paths": paths},
        )
        result: dict[str, Optional[str]] = {}
        for item in payload.get("files", []):
            if item.get("found"):
                result[str(item.get("path", ""))] = self._decode_bytes(item.get("content", "")).decode(encoding)
            else:
                result[str(item.get("path", ""))] = None
        return result

    def write_many(self, files: dict[str, str | bytes], *, encoding: str = "utf-8") -> list[WriteResult]:
        payload = self._client.request_json(
            "POST",
            f"/v1/fs/workspaces/{parse.quote(self.workspace_id, safe='')}:writeFiles",
            payload={
                "workspaceId": self.workspace_id,
                "files": [
                    {"path": path, "content": self._encode_bytes(content, encoding=encoding)}
                    for path, content in files.items()
                ],
            },
        )
        commit_hash = str(payload.get("commitHash", ""))
        results = []
        for item in payload.get("files", []):
            results.append(
                WriteResult(
                    workspace_id=self.workspace_id,
                    path=str(item.get("path", "")),
                    size=int(item.get("size", 0)),
                    hash=str(item.get("hash", "")),
                    commit_hash=commit_hash,
                )
            )
        return results

    def glob(self, pattern: str) -> list[str]:
        payload = self._client.request_json(
            "GET",
            f"/v1/fs/workspaces/{parse.quote(self.workspace_id, safe='')}:glob",
            params={"pattern": pattern},
        )
        return [str(item) for item in payload.get("paths", [])]

    def search(self, query: str, *, glob: Optional[str] = None) -> list[SearchMatch]:
        params = {"query": query}
        if glob:
            params["glob"] = glob
        payload = self._client.request_json(
            "GET",
            f"/v1/fs/workspaces/{parse.quote(self.workspace_id, safe='')}:search",
            params=params,
        )
        return [SearchMatch.from_api(item) for item in payload.get("matches", [])]

    def snapshot(self, message: str) -> SnapshotInfo:
        payload = self._client.request_json(
            "POST",
            f"/v1/fs/workspaces/{parse.quote(self.workspace_id, safe='')}/snapshot",
            payload={"workspaceId": self.workspace_id, "message": message},
        )
        return SnapshotInfo.from_api(payload.get("snapshot", {}))

    def snapshots(self, *, limit: Optional[int] = None) -> list[SnapshotInfo]:
        payload = self._client.request_json(
            "GET",
            f"/v1/fs/workspaces/{parse.quote(self.workspace_id, safe='')}/snapshots",
            params={"limit": limit},
        )
        return [SnapshotInfo.from_api(item) for item in payload.get("snapshots", [])]

    def restore(self, snapshot_id: str, *, message: str = "") -> SnapshotInfo:
        payload = self._client.request_json(
            "POST",
            f"/v1/fs/workspaces/{parse.quote(self.workspace_id, safe='')}/restore/{parse.quote(snapshot_id, safe='')}",
            payload={
                "workspaceId": self.workspace_id,
                "snapshotId": snapshot_id,
                "message": message,
            },
        )
        return SnapshotInfo.from_api(payload.get("snapshot", {}))

    def diff(self, from_snapshot_id: str, *, to_snapshot_id: Optional[str] = None, include_patches: bool = True) -> DiffResult:
        payload = self._client.request_json(
            "POST",
            f"/v1/fs/workspaces/{parse.quote(self.workspace_id, safe='')}:diff",
            payload={
                "workspaceId": self.workspace_id,
                "fromSnapshotId": from_snapshot_id,
                "toSnapshotId": to_snapshot_id or "",
                "includePatches": include_patches,
            },
        )
        return DiffResult.from_api(payload)

    def fork(
        self,
        fork_workspace_id: str,
        *,
        name: Optional[str] = None,
        description: str = "",
        snapshot_id: Optional[str] = None,
        cleanup_on_exit: bool = False,
    ) -> "Workspace":
        self._client.request_json(
            "POST",
            f"/v1/fs/workspaces/{parse.quote(self.workspace_id, safe='')}/fork",
            payload={
                "workspaceId": self.workspace_id,
                "forkWorkspaceId": fork_workspace_id,
                "name": name or fork_workspace_id,
                "description": description,
                "snapshotId": snapshot_id or "",
            },
        )
        return Workspace(self._client, fork_workspace_id, cleanup_on_exit=cleanup_on_exit)

    def merge(
        self,
        source_workspace: str | "Workspace",
        *,
        source_snapshot_id: Optional[str] = None,
        target_snapshot_id: Optional[str] = None,
        base_workspace_id: Optional[str] = None,
        base_snapshot_id: Optional[str] = None,
        message: str = "",
    ) -> MergeResult:
        source_workspace_id = (
            source_workspace.workspace_id if isinstance(source_workspace, Workspace) else source_workspace
        )
        payload = self._client.request_json(
            "POST",
            f"/v1/fs/workspaces/{parse.quote(self.workspace_id, safe='')}/merge",
            payload={
                "workspaceId": self.workspace_id,
                "sourceWorkspaceId": source_workspace_id,
                "sourceSnapshotId": source_snapshot_id or "",
                "targetSnapshotId": target_snapshot_id or "",
                "baseWorkspaceId": base_workspace_id or "",
                "baseSnapshotId": base_snapshot_id or "",
                "message": message,
            },
        )
        return MergeResult.from_api(payload)

    def conflicts(self, *, other_workspace_id: Optional[str] = None) -> list[WorkspaceConflict]:
        params = {"otherWorkspaceId": other_workspace_id} if other_workspace_id else None
        payload = self._client.request_json(
            "GET",
            f"/v1/fs/workspaces/{parse.quote(self.workspace_id, safe='')}/conflicts",
            params=params,
        )
        return [WorkspaceConflict.from_api(item) for item in payload.get("conflicts", [])]

    def resolve_conflict(
        self,
        path: str,
        *,
        preferred_workspace: Optional[str | "Workspace"] = None,
    ) -> WorkspaceConflict:
        preferred_workspace_id = (
            preferred_workspace.workspace_id
            if isinstance(preferred_workspace, Workspace)
            else preferred_workspace
        )
        payload = self._client.request_json(
            "POST",
            f"/v1/fs/workspaces/{parse.quote(self.workspace_id, safe='')}/conflicts:resolve",
            payload={
                "workspaceId": self.workspace_id,
                "path": path,
                "preferredWorkspaceId": preferred_workspace_id or self.workspace_id,
            },
        )
        return WorkspaceConflict.from_api(payload.get("conflict", {}))

    def _file_path(self, path: str) -> str:
        return f"/v1/fs/workspaces/{parse.quote(self.workspace_id, safe='')}/files/{parse.quote(path.strip('/'), safe='/')}"

    def _path_route(self, route: str, path: str) -> str:
        base = f"/v1/fs/workspaces/{parse.quote(self.workspace_id, safe='')}/{route}"
        if not path:
            return base
        return f"{base}/{parse.quote(path.strip('/'), safe='/')}"

    @staticmethod
    def _encode_bytes(content: str | bytes, *, encoding: str) -> str:
        data = content.encode(encoding) if isinstance(content, str) else content
        return base64.b64encode(data).decode("ascii")

    @staticmethod
    def _decode_bytes(content: str) -> bytes:
        return base64.b64decode(content)
