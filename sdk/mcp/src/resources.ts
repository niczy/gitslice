import { ResourceTemplate } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { RequestHandlerExtra } from "@modelcontextprotocol/sdk/shared/protocol.js";
import type { ServerNotification, ServerRequest } from "@modelcontextprotocol/sdk/types.js";

import type { GitsliceApi } from "./tools.js";

type ResourceContentsResult = {
  contents: Array<{ uri: string; mimeType?: string; text: string }>;
};

export function registerGitsliceResources(
  server: Pick<McpServer, "registerResource">,
  api: GitsliceApi,
  defaultWorkspaceId?: string,
): void {
  if (!defaultWorkspaceId) {
    return;
  }

  server.registerResource(
    "gitslice_workspace_tree",
    "gitslice://workspace/tree",
    {
      title: "Workspace Tree",
      description: "Recursive workspace path listing",
      mimeType: "application/json",
    },
    async (uri) => {
      const listing = await api.glob(defaultWorkspaceId, "**/*");
      return {
        contents: [
          {
            uri: uri.href,
            mimeType: "application/json",
            text: JSON.stringify(listing, null, 2),
          },
        ],
      };
    },
  );

  server.registerResource(
    "gitslice_file",
    new ResourceTemplate("gitslice://file/{path}", { list: undefined }),
    {
      title: "Workspace File",
      description: "Read a workspace file by URL-encoded path",
      mimeType: "text/plain",
    },
    async (uri, params) => {
      const rawPath = Array.isArray(params.path) ? params.path[0] : params.path;
      const path = decodeURIComponent(rawPath ?? "");
      const file = await api.read(defaultWorkspaceId, path);
      return {
        contents: [
          {
            uri: uri.href,
            mimeType: "text/plain",
            text: file.content,
          },
        ],
      };
    },
  );
}
