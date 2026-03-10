#!/usr/bin/env node

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";

import { registerGitsliceResources } from "./resources.js";
import { registerGitsliceTools, RestGitsliceApi } from "./tools.js";

export async function main(): Promise<void> {
  const server = new McpServer({
    name: "gitslice-mcp",
    version: "0.1.0",
  });

  const api = new RestGitsliceApi({
    baseUrl: process.env.GITSLICE_BASE_URL,
    username: process.env.GITSLICE_USERNAME,
    apiKey: process.env.GITSLICE_API_KEY,
  });
  const defaultWorkspaceId = process.env.GITSLICE_WORKSPACE;

  registerGitsliceTools(server, api, { defaultWorkspaceId });
  registerGitsliceResources(server, api, defaultWorkspaceId);

  const transport = new StdioServerTransport();
  await server.connect(transport);
}

const entrypoint = process.argv[1];
if (entrypoint) {
  const isDirectRun = import.meta.url === new URL(`file://${entrypoint}`).href;
  if (isDirectRun) {
    main().catch((error) => {
      console.error(error instanceof Error ? error.message : String(error));
      process.exitCode = 1;
    });
  }
}
