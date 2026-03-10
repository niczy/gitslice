import { HomeFilesystem } from "./home.js";
import { Workspace } from "./workspace.js";
import { parseWorkspaceInfo, type WorkspaceInfo } from "./types.js";

type ApiRecord = Record<string, unknown>;

export class GitsliceError extends Error {
  readonly statusCode: number;

  constructor(message: string, statusCode = 0) {
    super(message);
    this.name = new.target.name;
    this.statusCode = statusCode;
  }
}

export class AuthenticationError extends GitsliceError {}
export class PermissionDeniedError extends GitsliceError {}
export class ValidationError extends GitsliceError {}
export class NotFoundError extends GitsliceError {}
export class ConflictError extends GitsliceError {}
export class ServerError extends GitsliceError {}

export interface ResponseLike {
  readonly status: number;
  text(): Promise<string>;
}

export type FetchLike = (input: string, init?: RequestInit) => Promise<ResponseLike>;

export interface GitsliceClientOptions {
  baseUrl?: string;
  username?: string;
  apiKey?: string;
  timeoutMs?: number;
  fetchFn?: FetchLike;
}

export interface WorkspaceOptions {
  name?: string;
  description?: string;
}

export class GitsliceClient {
  private readonly baseUrl: string;
  private readonly timeoutMs: number;
  private readonly fetchFn: FetchLike;
  private readonly authHeader: string;
  private username?: string;

  constructor(options: GitsliceClientOptions = {}) {
    const username = (options.username ?? "").trim();
    const apiKey = (options.apiKey ?? "").trim();
    if (!username && !apiKey) {
      throw new Error("username or apiKey is required");
    }

    this.baseUrl = (options.baseUrl ?? "http://127.0.0.1:8080").replace(/\/+$/, "");
    this.timeoutMs = options.timeoutMs ?? 30_000;
    this.fetchFn = options.fetchFn ?? ((input, init) => fetch(input, init));
    this.authHeader = GitsliceClient.buildAuthHeader(username, apiKey);
    this.username = username || undefined;
  }

  async createWorkspace(workspaceId: string, options: WorkspaceOptions = {}): Promise<WorkspaceInfo> {
    const payload = await this.requestJson("POST", "/v1/fs/workspaces", {
      workspaceId,
      name: options.name ?? workspaceId,
      description: options.description ?? "",
    });
    return parseWorkspaceInfo(payload);
  }

  async deleteWorkspace(workspaceId: string): Promise<ApiRecord> {
    return this.requestJson("DELETE", `/v1/fs/workspaces/${encodeURIComponent(workspaceId)}`);
  }

  async getWorkspaceInfo(workspaceId: string): Promise<WorkspaceInfo> {
    const payload = await this.requestJson("GET", `/v1/fs/workspaces/${encodeURIComponent(workspaceId)}`);
    return parseWorkspaceInfo(payload);
  }

  async listWorkspaces(limit?: number, offset?: number): Promise<WorkspaceInfo[]> {
    const params: Record<string, string | number> = {};
    if (limit !== undefined) {
      params.limit = limit;
    }
    if (offset !== undefined) {
      params.offset = offset;
    }
    const payload = await this.requestJson("GET", "/v1/fs/workspaces", undefined, params);
    const workspaces = Array.isArray(payload.workspaces) ? payload.workspaces : [];
    return workspaces.map((item) => parseWorkspaceInfo((item as ApiRecord) ?? {}));
  }

  async workspace(workspaceId: string, options: WorkspaceOptions = {}): Promise<Workspace> {
    try {
      await this.getWorkspaceInfo(workspaceId);
    } catch (error) {
      if (!(error instanceof NotFoundError)) {
        throw error;
      }
      await this.createWorkspace(workspaceId, options);
    }
    return new Workspace(this, workspaceId);
  }

  async home(): Promise<HomeFilesystem> {
    return new HomeFilesystem(this, await this.resolveUsername());
  }

  async requestJson(
    method: string,
    path: string,
    payload?: ApiRecord,
    params?: Record<string, string | number | undefined>,
  ): Promise<ApiRecord> {
    const url = this.buildUrl(path, params);
    const headers = new Headers({
      Accept: "application/json",
      Authorization: this.authHeader,
      "User-Agent": "gitslice-typescript/0.1.0",
    });

    let body: string | undefined;
    if (payload !== undefined) {
      headers.set("Content-Type", "application/json");
      body = JSON.stringify(payload);
    }

    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), this.timeoutMs);
    try {
      const response = await this.fetchFn(url, {
        method,
        headers,
        body,
        signal: controller.signal,
      });
      const raw = await response.text();
      const data = raw ? GitsliceClient.parsePayload(raw) : {};
      if (response.status >= 200 && response.status < 300) {
        return data;
      }
      GitsliceClient.raiseError(response.status, data);
      throw new ServerError("unexpected response", response.status);
    } catch (error) {
      if (error instanceof GitsliceError) {
        throw error;
      }
      if (error instanceof Error && error.name === "AbortError") {
        throw new ServerError(`request timed out after ${this.timeoutMs}ms`);
      }
      throw new ServerError(`request failed: ${String(error)}`);
    } finally {
      clearTimeout(timeout);
    }
  }

  private buildUrl(path: string, params?: Record<string, string | number | undefined>): string {
    const url = new URL(`${this.baseUrl}${path}`);
    if (!params) {
      return url.toString();
    }
    for (const [key, value] of Object.entries(params)) {
      if (value !== undefined) {
        url.searchParams.set(key, String(value));
      }
    }
    return url.toString();
  }

  private async resolveUsername(): Promise<string> {
    if (this.username) {
      return this.username;
    }
    const payload = await this.requestJson("GET", "/v1/users/me");
    const username = String(payload.username ?? "").trim();
    if (!username) {
      throw new ServerError("current user did not include a username");
    }
    this.username = username;
    return username;
  }

  private static buildAuthHeader(username: string, apiKey: string): string {
    if (username) {
      return `User ${username}`;
    }
    const lower = apiKey.toLowerCase();
    if (lower.startsWith("bearer ") || lower.startsWith("user ")) {
      return apiKey;
    }
    return `Bearer ${apiKey}`;
  }

  private static raiseError(statusCode: number, payload: ApiRecord): never {
    const message = String(payload.message ?? payload.error ?? `http ${statusCode}`);
    if (statusCode === 400) {
      throw new ValidationError(message, statusCode);
    }
    if (statusCode === 401) {
      throw new AuthenticationError(message, statusCode);
    }
    if (statusCode === 403) {
      throw new PermissionDeniedError(message, statusCode);
    }
    if (statusCode === 404) {
      throw new NotFoundError(message, statusCode);
    }
    if (statusCode === 409) {
      throw new ConflictError(message, statusCode);
    }
    if (statusCode >= 500) {
      throw new ServerError(message, statusCode);
    }
    throw new GitsliceError(message, statusCode);
  }

  private static parsePayload(raw: string): ApiRecord {
    try {
      return JSON.parse(raw) as ApiRecord;
    } catch {
      return { message: raw };
    }
  }
}
