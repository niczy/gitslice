import { z } from "zod";

type ApiRecord = Record<string, unknown>;

export interface ReadFileResult {
  workspaceId: string;
  path: string;
  content: string;
  size?: number;
  hash?: string;
}

export interface GitsliceApi {
  read(workspaceId: string, path: string): Promise<ReadFileResult>;
  write(workspaceId: string, path: string, content: string): Promise<ApiRecord>;
  ls(workspaceId: string, path?: string): Promise<ApiRecord>;
  mkdir(workspaceId: string, path: string): Promise<ApiRecord>;
  rm(workspaceId: string, path: string): Promise<ApiRecord>;
  mv(workspaceId: string, sourcePath: string, destinationPath: string): Promise<ApiRecord>;
  search(workspaceId: string, query: string, glob?: string): Promise<ApiRecord>;
  glob(workspaceId: string, pattern: string): Promise<ApiRecord>;
  snapshot(workspaceId: string, message: string): Promise<ApiRecord>;
  diff(workspaceId: string, fromSnapshotId: string, toSnapshotId?: string, includePatches?: boolean): Promise<ApiRecord>;
  fork(workspaceId: string, forkWorkspaceId: string, description?: string): Promise<ApiRecord>;
  merge(workspaceId: string, sourceWorkspaceId: string, message?: string): Promise<ApiRecord>;
}

export interface ToolServer {
  registerTool(
    name: string,
    definition: {
      description?: string;
      inputSchema?: Record<string, z.ZodTypeAny>;
      outputSchema?: Record<string, z.ZodTypeAny>;
    },
    handler: (args: Record<string, unknown>) => Promise<{ content: Array<{ type: "text"; text: string }>; isError?: boolean }>,
  ): void;
}

export interface RegisterToolOptions {
  defaultWorkspaceId?: string;
}

export function registerGitsliceTools(server: ToolServer, api: GitsliceApi, options: RegisterToolOptions = {}): void {
  const workspaceId = workspaceResolver(options.defaultWorkspaceId);

  server.registerTool(
    "gitslice_read",
    {
      description: "Read file content from a Gitslice workspace",
      inputSchema: {
        workspaceId: z.string().optional(),
        path: z.string(),
      },
    },
    async (args) => handleResult(async () => {
      const result = await api.read(workspaceId(args.workspaceId), asString(args.path, "path"));
      return { content: [{ type: "text", text: result.content }] };
    }),
  );

  server.registerTool(
    "gitslice_write",
    {
      description: "Write file content to a Gitslice workspace",
      inputSchema: {
        workspaceId: z.string().optional(),
        path: z.string(),
        content: z.string(),
      },
    },
    async (args) => handleResult(async () => {
      const result = await api.write(
        workspaceId(args.workspaceId),
        asString(args.path, "path"),
        asString(args.content, "content"),
      );
      return textJson(result);
    }),
  );

  server.registerTool(
    "gitslice_ls",
    {
      description: "List directory entries in a Gitslice workspace",
      inputSchema: {
        workspaceId: z.string().optional(),
        path: z.string().optional(),
      },
    },
    async (args) => handleResult(async () => textJson(await api.ls(workspaceId(args.workspaceId), optionalString(args.path)))),
  );

  server.registerTool(
    "gitslice_mkdir",
    {
      description: "Create a directory in a Gitslice workspace",
      inputSchema: {
        workspaceId: z.string().optional(),
        path: z.string(),
      },
    },
    async (args) => handleResult(async () => textJson(await api.mkdir(workspaceId(args.workspaceId), asString(args.path, "path")))),
  );

  server.registerTool(
    "gitslice_rm",
    {
      description: "Delete a file from a Gitslice workspace",
      inputSchema: {
        workspaceId: z.string().optional(),
        path: z.string(),
      },
    },
    async (args) => handleResult(async () => textJson(await api.rm(workspaceId(args.workspaceId), asString(args.path, "path")))),
  );

  server.registerTool(
    "gitslice_mv",
    {
      description: "Move or rename a file in a Gitslice workspace",
      inputSchema: {
        workspaceId: z.string().optional(),
        sourcePath: z.string(),
        destinationPath: z.string(),
      },
    },
    async (args) =>
      handleResult(async () =>
        textJson(
          await api.mv(
            workspaceId(args.workspaceId),
            asString(args.sourcePath, "sourcePath"),
            asString(args.destinationPath, "destinationPath"),
          ),
        ),
      ),
  );

  server.registerTool(
    "gitslice_search",
    {
      description: "Search file contents in a Gitslice workspace",
      inputSchema: {
        workspaceId: z.string().optional(),
        query: z.string(),
        glob: z.string().optional(),
      },
    },
    async (args) =>
      handleResult(async () =>
        textJson(await api.search(workspaceId(args.workspaceId), asString(args.query, "query"), optionalString(args.glob))),
      ),
  );

  server.registerTool(
    "gitslice_glob",
    {
      description: "Find files by glob pattern in a Gitslice workspace",
      inputSchema: {
        workspaceId: z.string().optional(),
        pattern: z.string(),
      },
    },
    async (args) =>
      handleResult(async () => textJson(await api.glob(workspaceId(args.workspaceId), asString(args.pattern, "pattern")))),
  );

  server.registerTool(
    "gitslice_snapshot",
    {
      description: "Create a version snapshot for a Gitslice workspace",
      inputSchema: {
        workspaceId: z.string().optional(),
        message: z.string(),
      },
    },
    async (args) =>
      handleResult(async () => textJson(await api.snapshot(workspaceId(args.workspaceId), asString(args.message, "message")))),
  );

  server.registerTool(
    "gitslice_diff",
    {
      description: "Show changes since a snapshot in a Gitslice workspace",
      inputSchema: {
        workspaceId: z.string().optional(),
        fromSnapshotId: z.string(),
        toSnapshotId: z.string().optional(),
        includePatches: z.boolean().optional(),
      },
    },
    async (args) =>
      handleResult(async () =>
        textJson(
          await api.diff(
            workspaceId(args.workspaceId),
            asString(args.fromSnapshotId, "fromSnapshotId"),
            optionalString(args.toSnapshotId),
            typeof args.includePatches === "boolean" ? args.includePatches : true,
          ),
        ),
      ),
  );

  server.registerTool(
    "gitslice_fork",
    {
      description: "Create an isolated branch workspace from an existing workspace",
      inputSchema: {
        workspaceId: z.string().optional(),
        forkWorkspaceId: z.string(),
        description: z.string().optional(),
      },
    },
    async (args) =>
      handleResult(async () =>
        textJson(
          await api.fork(
            workspaceId(args.workspaceId),
            asString(args.forkWorkspaceId, "forkWorkspaceId"),
            optionalString(args.description),
          ),
        ),
      ),
  );

  server.registerTool(
    "gitslice_merge",
    {
      description: "Merge one workspace back into another",
      inputSchema: {
        workspaceId: z.string().optional(),
        sourceWorkspaceId: z.string(),
        message: z.string().optional(),
      },
    },
    async (args) =>
      handleResult(async () =>
        textJson(
          await api.merge(
            workspaceId(args.workspaceId),
            asString(args.sourceWorkspaceId, "sourceWorkspaceId"),
            optionalString(args.message),
          ),
        ),
      ),
  );
}

export class RestGitsliceApi implements GitsliceApi {
  private readonly baseUrl: string;
  private readonly authHeader: string;

  constructor(options: { baseUrl?: string; username?: string; apiKey?: string }) {
    const username = (options.username ?? "").trim();
    const apiKey = (options.apiKey ?? "").trim();
    if (!username && !apiKey) {
      throw new Error("GITSLICE_USERNAME or GITSLICE_API_KEY is required");
    }

    this.baseUrl = (options.baseUrl ?? "http://127.0.0.1:8080").replace(/\/+$/, "");
    this.authHeader = username
      ? `User ${username}`
      : apiKey.toLowerCase().startsWith("bearer ") || apiKey.toLowerCase().startsWith("user ")
        ? apiKey
        : `Bearer ${apiKey}`;
  }

  read(workspaceId: string, path: string): Promise<ReadFileResult> {
    return this.requestJson("GET", `/v1/fs/workspaces/${encodeURIComponent(workspaceId)}/files/${encodePath(path)}`).then(
      (payload) => ({
        workspaceId: String(payload.workspaceId ?? ""),
        path: String(payload.path ?? ""),
        content: decodeBase64(String(payload.content ?? "")),
        size: numberOrUndefined(payload.size),
        hash: stringOrUndefined(payload.hash),
      }),
    );
  }

  write(workspaceId: string, path: string, content: string): Promise<ApiRecord> {
    return this.requestJson("PUT", `/v1/fs/workspaces/${encodeURIComponent(workspaceId)}/files/${encodePath(path)}`, {
      workspaceId,
      path,
      content: encodeBase64(content),
    });
  }

  ls(workspaceId: string, path?: string): Promise<ApiRecord> {
    let requestPath = `/v1/fs/workspaces/${encodeURIComponent(workspaceId)}/ls`;
    if (path) {
      requestPath = `${requestPath}/${encodePath(path)}`;
    }
    return this.requestJson("GET", requestPath);
  }

  mkdir(workspaceId: string, path: string): Promise<ApiRecord> {
    return this.requestJson("POST", `/v1/fs/workspaces/${encodeURIComponent(workspaceId)}/mkdir/${encodePath(path)}`, {
      workspaceId,
      path,
    });
  }

  rm(workspaceId: string, path: string): Promise<ApiRecord> {
    return this.requestJson("DELETE", `/v1/fs/workspaces/${encodeURIComponent(workspaceId)}/files/${encodePath(path)}`);
  }

  mv(workspaceId: string, sourcePath: string, destinationPath: string): Promise<ApiRecord> {
    return this.requestJson("POST", `/v1/fs/workspaces/${encodeURIComponent(workspaceId)}:move`, {
      workspaceId,
      sourcePath,
      destinationPath,
    });
  }

  search(workspaceId: string, query: string, glob?: string): Promise<ApiRecord> {
    return this.requestJson("GET", `/v1/fs/workspaces/${encodeURIComponent(workspaceId)}:search`, undefined, {
      query,
      glob,
    });
  }

  glob(workspaceId: string, pattern: string): Promise<ApiRecord> {
    return this.requestJson("GET", `/v1/fs/workspaces/${encodeURIComponent(workspaceId)}:glob`, undefined, { pattern });
  }

  snapshot(workspaceId: string, message: string): Promise<ApiRecord> {
    return this.requestJson("POST", `/v1/fs/workspaces/${encodeURIComponent(workspaceId)}/snapshot`, {
      workspaceId,
      message,
    });
  }

  diff(workspaceId: string, fromSnapshotId: string, toSnapshotId?: string, includePatches = true): Promise<ApiRecord> {
    return this.requestJson("POST", `/v1/fs/workspaces/${encodeURIComponent(workspaceId)}:diff`, {
      workspaceId,
      fromSnapshotId,
      toSnapshotId: toSnapshotId ?? "",
      includePatches,
    });
  }

  fork(workspaceId: string, forkWorkspaceId: string, description?: string): Promise<ApiRecord> {
    return this.requestJson("POST", `/v1/fs/workspaces/${encodeURIComponent(workspaceId)}/fork`, {
      workspaceId,
      forkWorkspaceId,
      name: forkWorkspaceId,
      description: description ?? "",
    });
  }

  merge(workspaceId: string, sourceWorkspaceId: string, message?: string): Promise<ApiRecord> {
    return this.requestJson("POST", `/v1/fs/workspaces/${encodeURIComponent(workspaceId)}/merge`, {
      workspaceId,
      sourceWorkspaceId,
      message: message ?? "",
    });
  }

  private async requestJson(
    method: string,
    path: string,
    payload?: ApiRecord,
    params?: Record<string, string | boolean | undefined>,
  ): Promise<ApiRecord> {
    const url = new URL(`${this.baseUrl}${path}`);
    if (params) {
      for (const [key, value] of Object.entries(params)) {
        if (value !== undefined) {
          url.searchParams.set(key, String(value));
        }
      }
    }

    const headers = new Headers({
      Accept: "application/json",
      Authorization: this.authHeader,
      "Content-Type": "application/json",
      "User-Agent": "gitslice-mcp/0.1.0",
    });

    const response = await fetch(url, {
      method,
      headers,
      body: payload ? JSON.stringify(payload) : undefined,
    });
    const raw = await response.text();
    const data = raw ? parsePayload(raw) : {};
    if (response.ok) {
      return data;
    }
    throw new Error(String(data.message ?? data.error ?? `http ${response.status}`));
  }
}

function workspaceResolver(defaultWorkspaceId?: string) {
  return (value: unknown): string => {
    const resolved = optionalString(value) ?? defaultWorkspaceId;
    if (!resolved) {
      throw new Error("workspaceId is required");
    }
    return resolved;
  };
}

function asString(value: unknown, field: string): string {
  if (typeof value !== "string" || value.length === 0) {
    throw new Error(`${field} is required`);
  }
  return value;
}

function optionalString(value: unknown): string | undefined {
  return typeof value === "string" && value.length > 0 ? value : undefined;
}

function textJson(payload: unknown) {
  return { content: [{ type: "text" as const, text: JSON.stringify(payload, null, 2) }] };
}

async function handleResult(
  callback: () => Promise<{ content: Array<{ type: "text"; text: string }>; isError?: boolean }>,
): Promise<{ content: Array<{ type: "text"; text: string }>; isError?: boolean }> {
  try {
    return await callback();
  } catch (error) {
    return {
      content: [{ type: "text", text: `Error: ${error instanceof Error ? error.message : String(error)}` }],
      isError: true,
    };
  }
}

function parsePayload(raw: string): ApiRecord {
  try {
    return JSON.parse(raw) as ApiRecord;
  } catch {
    return { message: raw };
  }
}

function encodePath(path: string): string {
  return path
    .split("/")
    .filter((segment) => segment.length > 0)
    .map((segment) => encodeURIComponent(segment))
    .join("/");
}

function encodeBase64(value: string): string {
  return Buffer.from(value, "utf8").toString("base64");
}

function decodeBase64(value: string): string {
  return Buffer.from(value, "base64").toString("utf8");
}

function stringOrUndefined(value: unknown): string | undefined {
  return typeof value === "string" && value.length > 0 ? value : undefined;
}

function numberOrUndefined(value: unknown): number | undefined {
  return value === undefined || value === null || value === "" ? undefined : Number(value);
}
