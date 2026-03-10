from __future__ import annotations

import base64
from typing import Any, Optional
from urllib import parse

from .types import DiffResult, SearchMatch, SnapshotInfo, WorkspaceEntry, WriteResult


class HomeFilesystem:
    def __init__(self, client: Any, username: str) -> None:
        self._client = client
        self.username = username.strip()
        self.workspace_id = f"home.{self.username}"
        self.root_path = f"/{self.username}"

    def read(self, path: str, *, encoding: str = "utf-8") -> str:
        return self.read_bytes(path).decode(encoding)

    def read_bytes(self, path: str) -> bytes:
        payload = self._client.request_json("GET", self._file_path(path))
        return self._decode_bytes(payload["content"])

    def write(self, path: str, content: str | bytes, *, encoding: str = "utf-8") -> WriteResult:
        normalized_path = self._absolute_path(path)
        payload = self._client.request_json(
            "PUT",
            self._file_path(normalized_path),
            payload={
                "workspaceId": self.workspace_id,
                "path": normalized_path,
                "content": self._encode_bytes(content, encoding=encoding),
            },
        )
        return WriteResult.from_api(payload)

    def rm(self, path: str) -> dict[str, Any]:
        return self._client.request_json("DELETE", self._file_path(path))

    def mkdir(self, path: str) -> dict[str, Any]:
        normalized_path = self._absolute_path(path)
        return self._client.request_json(
            "POST",
            self._path_route("mkdir", normalized_path),
            payload={"workspaceId": self.workspace_id, "path": normalized_path},
        )

    def ls(self, path: str = "") -> list[WorkspaceEntry]:
        payload = self._client.request_json("GET", self._path_route("ls", path, required=False))
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
                "sourcePath": self._absolute_path(source_path),
                "destinationPath": self._absolute_path(destination_path),
            },
        )

    def cp(self, source_path: str, destination_path: str) -> dict[str, Any]:
        return self._client.request_json(
            "POST",
            f"/v1/fs/workspaces/{parse.quote(self.workspace_id, safe='')}:copy",
            payload={
                "workspaceId": self.workspace_id,
                "sourcePath": self._absolute_path(source_path),
                "destinationPath": self._absolute_path(destination_path),
            },
        )

    def read_many(self, paths: list[str], *, encoding: str = "utf-8") -> dict[str, Optional[str]]:
        payload = self._client.request_json(
            "POST",
            f"/v1/fs/workspaces/{parse.quote(self.workspace_id, safe='')}:readFiles",
            payload={"workspaceId": self.workspace_id, "paths": [self._absolute_path(path) for path in paths]},
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
                    {"path": self._absolute_path(path), "content": self._encode_bytes(content, encoding=encoding)}
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
            params={"pattern": self._absolute_path(pattern)},
        )
        return [str(item) for item in payload.get("paths", [])]

    def search(self, query: str, *, glob: Optional[str] = None) -> list[SearchMatch]:
        params = {"query": query}
        if glob:
            params["glob"] = self._absolute_path(glob)
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

    def _file_path(self, path: str) -> str:
        return f"/v1/fs/workspaces/{parse.quote(self.workspace_id, safe='')}/files/{self._encode_absolute_path(path)}"

    def _path_route(self, route: str, path: str, *, required: bool = True) -> str:
        normalized_path = self._absolute_path(path, required=required)
        base = f"/v1/fs/workspaces/{parse.quote(self.workspace_id, safe='')}/{route}"
        if not normalized_path:
            return base
        return f"{base}/{self._encode_absolute_path(normalized_path)}"

    def _absolute_path(self, path: str, *, required: bool = True) -> str:
        value = (path or "").strip()
        if not value:
            if required:
                raise ValueError("absolute path is required")
            return self.root_path
        if not value.startswith("/"):
            raise ValueError("absolute path is required")
        return value

    @staticmethod
    def _encode_absolute_path(path: str) -> str:
        return parse.quote(path, safe="")

    @staticmethod
    def _encode_bytes(content: str | bytes, *, encoding: str) -> str:
        data = content.encode(encoding) if isinstance(content, str) else content
        return base64.b64encode(data).decode("ascii")

    @staticmethod
    def _decode_bytes(content: str) -> bytes:
        return base64.b64decode(content)
