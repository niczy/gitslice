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

test("home filesystem uses absolute paths and home workspace", async () => {
  const transport = createFetch([
    {
      status: 200,
      payload: {
        workspaceId: "home_tester",
        path: "/tester/README.md",
        size: "6",
        hash: "hash",
        commitHash: "commit-1",
      },
    },
    {
      status: 200,
      payload: {
        workspaceId: "home_tester",
        path: "/tester/README.md",
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

  const home = await client.home();
  const writeResult = await home.write("/tester/README.md", "hello\n");
  const content = await home.read("/tester/README.md");
  const writeBody = JSON.parse(transport.calls[0].init.body);

  assert.equal(home.workspaceId, "home_tester");
  assert.equal(writeResult.workspaceId, "home_tester");
  assert.equal(content, "hello\n");
  assert.equal(writeBody.workspaceId, "home_tester");
  assert.equal(writeBody.path, "/tester/README.md");
  assert.match(transport.calls[0].input, /\/v1\/fs\/workspaces\/home_tester\/files\/%2Ftester%2FREADME\.md$/);
  assert.match(transport.calls[1].input, /\/v1\/fs\/workspaces\/home_tester\/files\/%2Ftester%2FREADME\.md$/);
});

test("home filesystem resolves username from current user", async () => {
  const transport = createFetch([
    {
      status: 200,
      payload: {
        id: "u1",
        username: "token-user",
        name: "Token User",
      },
    },
    {
      status: 200,
      payload: {
        entries: [],
      },
    },
  ]);
  const client = new GitsliceClient({
    baseUrl: "https://example.test",
    apiKey: "token",
    fetchFn: transport.fetchFn,
  });

  const home = await client.home();
  const entries = await home.ls();

  assert.deepEqual(entries, []);
  assert.equal(home.workspaceId, "home_token-user");
  assert.equal(transport.calls[0].input, "https://example.test/v1/users/me");
  assert.match(transport.calls[1].input, /\/v1\/fs\/workspaces\/home_token-user\/ls\/%2Ftoken-user$/);
});

test("home filesystem requires absolute paths", async () => {
  const client = new GitsliceClient({
    baseUrl: "https://example.test",
    username: "tester",
    fetchFn: async () => {
      throw new Error("should not be called");
    },
  });
  const home = await client.home();

  await assert.rejects(() => home.write("README.md", "hello\n"), /absolute path is required/);
});
