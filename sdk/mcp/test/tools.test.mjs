import test from "node:test";
import assert from "node:assert/strict";

import { registerGitsliceTools } from "../dist/tools.js";

class FakeServer {
  constructor() {
    this.tools = new Map();
  }

  registerTool(name, definition, handler) {
    this.tools.set(name, { definition, handler });
  }
}

test("registers planned MCP filesystem tools", () => {
  const server = new FakeServer();
  registerGitsliceTools(server, {});

  assert.deepEqual([...server.tools.keys()].sort(), [
    "gitslice_diff",
    "gitslice_fork",
    "gitslice_glob",
    "gitslice_ls",
    "gitslice_merge",
    "gitslice_mkdir",
    "gitslice_mv",
    "gitslice_read",
    "gitslice_rm",
    "gitslice_search",
    "gitslice_snapshot",
    "gitslice_write",
  ]);
});

test("read tool calls api and returns text content", async () => {
  const server = new FakeServer();
  const api = {
    async read(workspaceId, path) {
      return { workspaceId, path, content: "hello from mcp" };
    },
  };
  registerGitsliceTools(server, api, { defaultWorkspaceId: "demo" });

  const result = await server.tools.get("gitslice_read").handler({ path: "README.md" });

  assert.equal(result.isError, undefined);
  assert.equal(result.content[0].text, "hello from mcp");
});

test("merge tool honors explicit workspace ids", async () => {
  const server = new FakeServer();
  let call;
  const api = {
    async merge(workspaceId, sourceWorkspaceId, message) {
      call = { workspaceId, sourceWorkspaceId, message };
      return { status: "MERGE_STATUS_SUCCESS" };
    },
  };
  registerGitsliceTools(server, api, { defaultWorkspaceId: "main" });

  const result = await server.tools.get("gitslice_merge").handler({
    workspaceId: "target",
    sourceWorkspaceId: "fork",
    message: "merge it",
  });

  assert.equal(result.isError, undefined);
  assert.deepEqual(call, {
    workspaceId: "target",
    sourceWorkspaceId: "fork",
    message: "merge it",
  });
});

test("tools return MCP errors for thrown api failures", async () => {
  const server = new FakeServer();
  const api = {
    async read() {
      throw new Error("boom");
    },
  };
  registerGitsliceTools(server, api, { defaultWorkspaceId: "demo" });

  const result = await server.tools.get("gitslice_read").handler({ path: "README.md" });

  assert.equal(result.isError, true);
  assert.match(result.content[0].text, /boom/);
});
