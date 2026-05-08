import type { GitsliceClient } from "./client.js";
import {
  parseDiffResult,
  parseSearchMatch,
  parseSnapshotInfo,
  parseWorkspaceEntry,
  type DiffResult,
  type SearchMatch,
  type SnapshotInfo,
  type WorkspaceEntry,
  type WriteResult,
} from "./types.js";

type ApiRecord = Record<string, unknown>;

const textEncoder = new TextEncoder();
const textDecoder = new TextDecoder();

interface BufferLike {
  toString(encoding: "base64"): string;
}

interface BufferConstructorLike {
  from(data: Uint8Array): BufferLike;
  from(data: string, encoding: "base64"): Uint8Array;
}

export class HomeFilesystem {
  readonly username: string;
  readonly workspaceId: string;
  readonly rootPath: string;

  constructor(private readonly client: GitsliceClient, username: string) {
    this.username = username.trim();
    this.workspaceId = `home_${this.username}`;
    this.rootPath = `/${this.username}`;
  }

  async read(path: string): Promise<string> {
    return textDecoder.decode(await this.readBytes(path));
  }

  async readBytes(path: string): Promise<Uint8Array> {
    const payload = await this.client.requestJson("GET", this.filePath(path));
    return decodeBytes(String(payload.content ?? ""));
  }

  async write(path: string, content: string | Uint8Array): Promise<WriteResult> {
    const normalizedPath = this.absolutePath(path);
    const payload = await this.client.requestJson("PUT", this.filePath(normalizedPath), {
      workspaceId: this.workspaceId,
      path: normalizedPath,
      content: encodeBytes(content),
    });
    return {
      workspaceId: String(payload.workspaceId ?? ""),
      path: String(payload.path ?? ""),
      size: Number(payload.size ?? 0),
      hash: String(payload.hash ?? ""),
      commitHash: String(payload.commitHash ?? ""),
    };
  }

  async rm(path: string): Promise<ApiRecord> {
    return this.client.requestJson("DELETE", this.filePath(path));
  }

  async mkdir(path: string): Promise<ApiRecord> {
    const normalizedPath = this.absolutePath(path);
    return this.client.requestJson(
      "POST",
      this.pathRoute("mkdir", normalizedPath),
      { workspaceId: this.workspaceId, path: normalizedPath },
    );
  }

  async ls(path = ""): Promise<WorkspaceEntry[]> {
    const payload = await this.client.requestJson("GET", this.pathRoute("ls", path, false));
    const entries = Array.isArray(payload.entries) ? payload.entries : [];
    return entries.map((item) => parseWorkspaceEntry((item as ApiRecord) ?? {}));
  }

  async exists(path: string): Promise<boolean> {
    const payload = await this.client.requestJson("GET", this.pathRoute("exists", path));
    return Boolean(payload.exists ?? false);
  }

  async stat(path: string): Promise<WorkspaceEntry | null> {
    const payload = await this.client.requestJson("GET", this.pathRoute("stat", path));
    if (!payload.exists) {
      return null;
    }
    return parseWorkspaceEntry((payload.entry as ApiRecord | undefined) ?? {});
  }

  async mv(sourcePath: string, destinationPath: string): Promise<ApiRecord> {
    return this.client.requestJson("POST", `/v1/fs/workspaces/${encodeURIComponent(this.workspaceId)}:move`, {
      workspaceId: this.workspaceId,
      sourcePath: this.absolutePath(sourcePath),
      destinationPath: this.absolutePath(destinationPath),
    });
  }

  async cp(sourcePath: string, destinationPath: string): Promise<ApiRecord> {
    return this.client.requestJson("POST", `/v1/fs/workspaces/${encodeURIComponent(this.workspaceId)}:copy`, {
      workspaceId: this.workspaceId,
      sourcePath: this.absolutePath(sourcePath),
      destinationPath: this.absolutePath(destinationPath),
    });
  }

  async readMany(paths: string[]): Promise<Record<string, string | null>> {
    const payload = await this.client.requestJson(
      "POST",
      `/v1/fs/workspaces/${encodeURIComponent(this.workspaceId)}:readFiles`,
      { workspaceId: this.workspaceId, paths: paths.map((path) => this.absolutePath(path)) },
    );
    const files = Array.isArray(payload.files) ? payload.files : [];
    const result: Record<string, string | null> = {};
    for (const file of files) {
      const item = (file as ApiRecord) ?? {};
      const pathKey = String(item.path ?? "");
      result[pathKey] = item.found ? textDecoder.decode(decodeBytes(String(item.content ?? ""))) : null;
    }
    return result;
  }

  async writeMany(files: Record<string, string | Uint8Array>): Promise<WriteResult[]> {
    const payload = await this.client.requestJson(
      "POST",
      `/v1/fs/workspaces/${encodeURIComponent(this.workspaceId)}:writeFiles`,
      {
        workspaceId: this.workspaceId,
        files: Object.entries(files).map(([path, content]) => ({
          path: this.absolutePath(path),
          content: encodeBytes(content),
        })),
      },
    );
    const results = Array.isArray(payload.files) ? payload.files : [];
    const commitHash = String(payload.commitHash ?? "");
    return results.map((file) => {
      const item = (file as ApiRecord) ?? {};
      return {
        workspaceId: this.workspaceId,
        path: String(item.path ?? ""),
        size: Number(item.size ?? 0),
        hash: String(item.hash ?? ""),
        commitHash,
      };
    });
  }

  async glob(pattern: string): Promise<string[]> {
    const payload = await this.client.requestJson(
      "GET",
      `/v1/fs/workspaces/${encodeURIComponent(this.workspaceId)}:glob`,
      undefined,
      { pattern: this.absolutePath(pattern) },
    );
    return Array.isArray(payload.paths) ? payload.paths.map((item) => String(item)) : [];
  }

  async search(query: string, glob?: string): Promise<SearchMatch[]> {
    const payload = await this.client.requestJson(
      "GET",
      `/v1/fs/workspaces/${encodeURIComponent(this.workspaceId)}:search`,
      undefined,
      { query, glob: glob ? this.absolutePath(glob) : undefined },
    );
    const matches = Array.isArray(payload.matches) ? payload.matches : [];
    return matches.map((item) => parseSearchMatch((item as ApiRecord) ?? {}));
  }

  async snapshot(message: string): Promise<SnapshotInfo> {
    const payload = await this.client.requestJson(
      "POST",
      `/v1/fs/workspaces/${encodeURIComponent(this.workspaceId)}/snapshot`,
      { workspaceId: this.workspaceId, message },
    );
    return parseSnapshotInfo((payload.snapshot as ApiRecord | undefined) ?? {});
  }

  async snapshots(limit?: number): Promise<SnapshotInfo[]> {
    const payload = await this.client.requestJson(
      "GET",
      `/v1/fs/workspaces/${encodeURIComponent(this.workspaceId)}/snapshots`,
      undefined,
      { limit },
    );
    const snapshots = Array.isArray(payload.snapshots) ? payload.snapshots : [];
    return snapshots.map((item) => parseSnapshotInfo((item as ApiRecord) ?? {}));
  }

  async restore(snapshotId: string, message = ""): Promise<SnapshotInfo> {
    const payload = await this.client.requestJson(
      "POST",
      `/v1/fs/workspaces/${encodeURIComponent(this.workspaceId)}/restore/${encodeURIComponent(snapshotId)}`,
      {
        workspaceId: this.workspaceId,
        snapshotId,
        message,
      },
    );
    return parseSnapshotInfo((payload.snapshot as ApiRecord | undefined) ?? {});
  }

  async diff(fromSnapshotId: string, toSnapshotId?: string, includePatches = true): Promise<DiffResult> {
    const payload = await this.client.requestJson(
      "POST",
      `/v1/fs/workspaces/${encodeURIComponent(this.workspaceId)}:diff`,
      {
        workspaceId: this.workspaceId,
        fromSnapshotId,
        toSnapshotId: toSnapshotId ?? "",
        includePatches,
      },
    );
    return parseDiffResult(payload);
  }

  private filePath(path: string): string {
    return `/v1/fs/workspaces/${encodeURIComponent(this.workspaceId)}/files/${encodeAbsolutePath(this.absolutePath(path))}`;
  }

  private pathRoute(route: string, path: string, required = true): string {
    const normalizedPath = this.absolutePath(path, required);
    const base = `/v1/fs/workspaces/${encodeURIComponent(this.workspaceId)}/${route}`;
    return normalizedPath ? `${base}/${encodeAbsolutePath(normalizedPath)}` : base;
  }

  private absolutePath(path: string, required = true): string {
    const value = path.trim();
    if (!value) {
      if (required) {
        throw new Error("absolute path is required");
      }
      return this.rootPath;
    }
    if (!value.startsWith("/")) {
      throw new Error("absolute path is required");
    }
    return value;
  }
}

function encodeAbsolutePath(path: string): string {
  return encodeURIComponent(path);
}

function encodeBytes(content: string | Uint8Array): string {
  const bytes = typeof content === "string" ? textEncoder.encode(content) : content;
  const nodeBuffer = (globalThis as typeof globalThis & { Buffer?: BufferConstructorLike }).Buffer;
  if (nodeBuffer) {
    return nodeBuffer.from(bytes).toString("base64");
  }
  let binary = "";
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary);
}

function decodeBytes(content: string): Uint8Array {
  const nodeBuffer = (globalThis as typeof globalThis & { Buffer?: BufferConstructorLike }).Buffer;
  if (nodeBuffer) {
    return new Uint8Array(nodeBuffer.from(content, "base64"));
  }
  const binary = atob(content);
  const bytes = new Uint8Array(binary.length);
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index);
  }
  return bytes;
}
