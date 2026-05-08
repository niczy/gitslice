import test from "node:test";
import assert from "node:assert/strict";

import {
  GitsliceClient,
  NotFoundError,
  ValidationError,
} from "../dist/index.js";

class FakeResponse {
  constructor(status, payload) {
    this.status = status;
    this.payload = payload;
  }

  async text() {
    return JSON.stringify(this.payload);
  }
}

function createFetch(responses) {
  const queue = [...responses];
  const calls = [];
  return {
    calls,
    fetchFn: async (input, init) => {
      calls.push({ input, init });
      const next = queue.shift();
      return new FakeResponse(next.status, next.payload);
    },
  };
}

test("workspace auto-creates on not found", async () => {
  const transport = createFetch([
    { status: 404, payload: { message: "workspace not found" } },
    {
      status: 200,
      payload: {
        workspaceId: "demo",
        name: "demo",
        description: "",
        createdBy: "tester",
        owners: ["tester"],
        headCommitHash: "cmt_init_demo",
        createdAt: "1",
        updatedAt: "1",
        pathCount: 0,
        fileCount: 0,
        isRoot: false,
      },
    },
  ]);
  const client = new GitsliceClient({
    baseUrl: "https://example.test",
    username: "tester",
    fetchFn: transport.fetchFn,
  });

  const workspace = await client.workspace("demo");

  assert.equal(workspace.workspaceId, "demo");
  assert.equal(transport.calls.length, 2);
  assert.equal(transport.calls[0].init.method, "GET");
  assert.equal(transport.calls[1].init.method, "POST");
});

test("create workspace serializes json", async () => {
  const transport = createFetch([
    {
      status: 200,
      payload: {
        workspaceId: "demo",
        name: "Demo",
        description: "desc",
        createdBy: "tester",
        owners: ["tester"],
        headCommitHash: "cmt_init_demo",
        createdAt: "1",
        updatedAt: "2",
        pathCount: 0,
        fileCount: 0,
        isRoot: false,
      },
    },
  ]);
  const client = new GitsliceClient({
    baseUrl: "https://example.test",
    username: "tester",
    fetchFn: transport.fetchFn,
  });

  const info = await client.createWorkspace("demo", { name: "Demo", description: "desc" });
  const body = JSON.parse(transport.calls[0].init.body);

  assert.equal(body.workspaceId, "demo");
  assert.equal(info.workspaceId, "demo");
  assert.equal(info.name, "Demo");
});

test("maps validation errors", async () => {
  const transport = createFetch([{ status: 400, payload: { message: "bad request" } }]);
  const client = new GitsliceClient({
    baseUrl: "https://example.test",
    username: "tester",
    fetchFn: transport.fetchFn,
  });

  await assert.rejects(() => client.listWorkspaces(-1), ValidationError);
});

test("maps not found errors", async () => {
  const transport = createFetch([{ status: 404, payload: { message: "missing" } }]);
  const client = new GitsliceClient({
    baseUrl: "https://example.test",
    username: "tester",
    fetchFn: transport.fetchFn,
  });

  await assert.rejects(() => client.getWorkspaceInfo("missing"), NotFoundError);
});
