import test from "node:test";
import assert from "node:assert/strict";

import { GitsliceClient } from "../dist/index.js";

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

test("write and read round trip", async () => {
  const transport = createFetch([
    {
      status: 200,
      payload: {
        workspaceId: "demo",
        name: "demo",
        description: "",
        createdBy: "tester",
        owners: ["tester"],
        headCommitHash: "init-demo",
        createdAt: "1",
        updatedAt: "1",
        pathCount: 0,
        fileCount: 0,
        isRoot: false,
      },
    },
    {
      status: 200,
      payload: {
        workspaceId: "demo",
        path: "README.md",
        size: "6",
        hash: "hash",
        commitHash: "commit-1",
      },
    },
    {
      status: 200,
      payload: {
        workspaceId: "demo",
        path: "README.md",
        content: Buffer.from("hello\n", "utf8").toString("base64"),
        size: "6",
        hash: "hash",
      },
    },
  ]);
  const client = new GitsliceClient({
    baseUrl: "https://example.test",
    username: "tester",
    fetchFn: transport.fetchFn,
  });
  const workspace = await client.workspace("demo");

  const writeResult = await workspace.write("README.md", "hello\n");
  const content = await workspace.read("README.md");
  const writeBody = JSON.parse(transport.calls[1].init.body);

  assert.equal(writeBody.content, Buffer.from("hello\n", "utf8").toString("base64"));
  assert.equal(writeResult.commitHash, "commit-1");
  assert.equal(content, "hello\n");
});

test("write many and search parse", async () => {
  const transport = createFetch([
    {
      status: 200,
      payload: {
        workspaceId: "demo",
        name: "demo",
        description: "",
        createdBy: "tester",
        owners: ["tester"],
        headCommitHash: "init-demo",
        createdAt: "1",
        updatedAt: "1",
        pathCount: 0,
        fileCount: 0,
        isRoot: false,
      },
    },
    {
      status: 200,
      payload: {
        workspaceId: "demo",
        files: [
          { path: "a.ts", size: "3", hash: "h1" },
          { path: "b.ts", size: "3", hash: "h2" },
        ],
        commitHash: "commit-batch",
      },
    },
    {
      status: 200,
      payload: {
        workspaceId: "demo",
        query: "main",
        glob: "**/*.ts",
        matches: [
          {
            path: "a.ts",
            lineNumber: "2",
            line: "export function main() {}",
            matchStart: "16",
            matchEnd: "20",
          },
        ],
      },
    },
  ]);
  const client = new GitsliceClient({
    baseUrl: "https://example.test",
    username: "tester",
    fetchFn: transport.fetchFn,
  });
  const workspace = await client.workspace("demo");

  const results = await workspace.writeMany({ "a.ts": "one", "b.ts": "two" });
  const matches = await workspace.search("main", "**/*.ts");

  assert.equal(results[0].commitHash, "commit-batch");
  assert.equal(matches[0].path, "a.ts");
  assert.equal(matches[0].lineNumber, 2);
});

test("merge accepts workspace instance", async () => {
  const transport = createFetch([
    {
      status: 200,
      payload: {
        workspaceId: "main",
        name: "main",
        description: "",
        createdBy: "tester",
        owners: ["tester"],
        headCommitHash: "init-main",
        createdAt: "1",
        updatedAt: "1",
        pathCount: 0,
        fileCount: 0,
        isRoot: false,
      },
    },
    {
      status: 200,
      payload: {
        workspaceId: "fork",
        name: "fork",
        description: "",
        createdBy: "tester",
        owners: ["tester"],
        headCommitHash: "init-fork",
        createdAt: "1",
        updatedAt: "1",
        pathCount: 0,
        fileCount: 0,
        isRoot: false,
      },
    },
    {
      status: 200,
      payload: {
        workspaceId: "main",
        sourceWorkspaceId: "fork",
        baseWorkspaceId: "main",
        baseSnapshotId: "base-1",
        sourceSnapshotId: "source-1",
        targetSnapshotId: "target-1",
        status: "MERGE_STATUS_SUCCESS",
        commitHash: "commit-1",
        mergedPaths: ["README.md"],
        summary: {
          filesAdded: 0,
          filesModified: 1,
          filesDeleted: 0,
          linesAdded: 1,
          linesDeleted: 0,
        },
        conflicts: [],
      },
    },
  ]);
  const client = new GitsliceClient({
    baseUrl: "https://example.test",
    username: "tester",
    fetchFn: transport.fetchFn,
  });
  const main = await client.workspace("main");
  const fork = await client.workspace("fork");

  const result = await main.merge(fork, { message: "merge fork" });
  const body = JSON.parse(transport.calls[2].init.body);

  assert.equal(body.sourceWorkspaceId, "fork");
  assert.equal(body.message, "merge fork");
  assert.equal(result.baseSnapshotId, "base-1");
  assert.deepEqual(result.mergedPaths, ["README.md"]);
});
