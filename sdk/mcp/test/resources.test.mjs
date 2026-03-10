import test from "node:test";
import assert from "node:assert/strict";

import { registerGitsliceResources } from "../dist/resources.js";

class FakeServer {
  constructor() {
    this.resources = new Map();
  }

  registerResource(name, uriOrTemplate, metadata, handler) {
    this.resources.set(name, { uriOrTemplate, metadata, handler });
  }
}

test("skips resource registration without a default workspace", () => {
  const server = new FakeServer();
  registerGitsliceResources(server, {});

  assert.equal(server.resources.size, 0);
});

test("registers workspace tree and file resources", () => {
  const server = new FakeServer();
  registerGitsliceResources(server, {}, "demo");

  assert.deepEqual([...server.resources.keys()].sort(), ["gitslice_file", "gitslice_workspace_tree"]);
});

test("workspace tree resource reads recursive listing", async () => {
  const server = new FakeServer();
  const api = {
    async glob(workspaceId, pattern) {
      assert.equal(workspaceId, "demo");
      assert.equal(pattern, "**/*");
      return { paths: ["README.md"] };
    },
  };
  registerGitsliceResources(server, api, "demo");

  const resource = server.resources.get("gitslice_workspace_tree");
  const result = await resource.handler(new URL("gitslice://workspace/tree"), {});

  assert.match(result.contents[0].text, /README\.md/);
});

test("file resource decodes path params and reads file text", async () => {
  const server = new FakeServer();
  const api = {
    async read(workspaceId, path) {
      assert.equal(workspaceId, "demo");
      assert.equal(path, "src/main.ts");
      return { content: "export const main = true;" };
    },
  };
  registerGitsliceResources(server, api, "demo");

  const resource = server.resources.get("gitslice_file");
  const result = await resource.handler(new URL("gitslice://file/src%2Fmain.ts"), {
    path: "src%2Fmain.ts",
  });

  assert.match(result.contents[0].text, /main = true/);
});
