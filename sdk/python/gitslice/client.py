from __future__ import annotations

import json
from typing import Any, Callable, Mapping, Optional
from urllib import error, parse, request

from .exceptions import (
    AuthenticationError,
    ConflictError,
    GitsliceError,
    NotFoundError,
    PermissionDeniedError,
    ServerError,
    ValidationError,
)
from .types import WorkspaceInfo
from .workspace import Workspace

Transport = Callable[[str, str, Mapping[str, str], Optional[bytes]], tuple[int, bytes]]


class GitsliceClient:
    def __init__(
        self,
        *,
        base_url: str = "http://127.0.0.1:8080",
        username: Optional[str] = None,
        api_key: Optional[str] = None,
        timeout: float = 30.0,
        transport: Optional[Transport] = None,
    ) -> None:
        identity = (username or "").strip()
        token = (api_key or "").strip()
        if not identity and not token:
            raise ValueError("username or api_key is required")

        self._base_url = base_url.rstrip("/")
        self._timeout = timeout
        self._transport = transport or self._default_transport
        self._auth_header = self._build_auth_header(identity, token)

    def create_workspace(self, workspace_id: str, *, name: Optional[str] = None, description: str = "") -> WorkspaceInfo:
        payload = {
            "workspaceId": workspace_id,
            "name": name or workspace_id,
            "description": description,
        }
        return WorkspaceInfo.from_api(self._request_json("POST", "/v1/fs/workspaces", payload=payload))

    def delete_workspace(self, workspace_id: str) -> dict[str, Any]:
        return self._request_json("DELETE", f"/v1/fs/workspaces/{parse.quote(workspace_id, safe='')}")

    def get_workspace_info(self, workspace_id: str) -> WorkspaceInfo:
        payload = self._request_json("GET", f"/v1/fs/workspaces/{parse.quote(workspace_id, safe='')}")
        return WorkspaceInfo.from_api(payload)

    def list_workspaces(self, *, limit: Optional[int] = None, offset: Optional[int] = None) -> list[WorkspaceInfo]:
        params: dict[str, Any] = {}
        if limit is not None:
            params["limit"] = limit
        if offset is not None:
            params["offset"] = offset
        payload = self._request_json("GET", "/v1/fs/workspaces", params=params or None)
        return [WorkspaceInfo.from_api(item) for item in payload.get("workspaces", [])]

    def workspace(
        self,
        workspace_id: str,
        *,
        name: Optional[str] = None,
        description: str = "",
        cleanup_on_exit: Optional[bool] = None,
    ) -> Workspace:
        created = False
        try:
            self.get_workspace_info(workspace_id)
        except NotFoundError:
            self.create_workspace(workspace_id, name=name, description=description)
            created = True
        if cleanup_on_exit is None:
            cleanup_on_exit = created
        return Workspace(self, workspace_id, cleanup_on_exit=cleanup_on_exit)

    def request_json(
        self,
        method: str,
        path: str,
        *,
        payload: Optional[Mapping[str, Any]] = None,
        params: Optional[Mapping[str, Any]] = None,
    ) -> dict[str, Any]:
        return self._request_json(method, path, payload=payload, params=params)

    def _request_json(
        self,
        method: str,
        path: str,
        *,
        payload: Optional[Mapping[str, Any]] = None,
        params: Optional[Mapping[str, Any]] = None,
    ) -> dict[str, Any]:
        url = self._build_url(path, params)
        headers = {
            "Accept": "application/json",
            "Authorization": self._auth_header,
            "User-Agent": "gitslice-python/0.1.0",
        }
        body: Optional[bytes] = None
        if payload is not None:
            headers["Content-Type"] = "application/json"
            body = json.dumps(payload).encode("utf-8")

        status_code, raw = self._transport(method, url, headers, body)
        if not raw:
            return {}
        data = json.loads(raw.decode("utf-8"))
        if 200 <= status_code < 300:
            return data
        self._raise_error(status_code, data)
        raise ServerError("unexpected response")

    def _default_transport(
        self,
        method: str,
        url: str,
        headers: Mapping[str, str],
        body: Optional[bytes],
    ) -> tuple[int, bytes]:
        req = request.Request(url=url, data=body, headers=dict(headers), method=method)
        try:
            with request.urlopen(req, timeout=self._timeout) as resp:
                return resp.getcode(), resp.read()
        except error.HTTPError as exc:
            return exc.code, exc.read()
        except error.URLError as exc:
            raise ServerError(f"request failed: {exc}") from exc

    def _build_url(self, path: str, params: Optional[Mapping[str, Any]]) -> str:
        url = f"{self._base_url}{path}"
        if not params:
            return url
        query = parse.urlencode({key: value for key, value in params.items() if value is not None})
        if not query:
            return url
        return f"{url}?{query}"

    @staticmethod
    def _build_auth_header(username: str, api_key: str) -> str:
        if username:
            return f"User {username}"
        if api_key.lower().startswith(("bearer ", "user ")):
            return api_key
        return f"Bearer {api_key}"

    @staticmethod
    def _raise_error(status_code: int, payload: Mapping[str, Any]) -> None:
        message = str(payload.get("message") or payload.get("error") or f"http {status_code}")
        if status_code == 400:
            raise ValidationError(message)
        if status_code == 401:
            raise AuthenticationError(message)
        if status_code == 403:
            raise PermissionDeniedError(message)
        if status_code == 404:
            raise NotFoundError(message)
        if status_code == 409:
            raise ConflictError(message)
        if status_code >= 500:
            raise ServerError(message)
        raise GitsliceError(message)
