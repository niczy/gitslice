# Gitslice TypeScript SDK

Thin TypeScript client for the Gitslice remote filesystem API.

## Install

```bash
cd sdk/typescript
npm install
```

## Usage

```ts
import { GitsliceClient } from "@gitslice/sdk";

const client = new GitsliceClient({
  baseUrl: "https://agenttools.dev",
  username: "tester",
});

const home = await client.home();
await home.write("/tester/README.md", "hello from typescript\n");
console.log(await home.read("/tester/README.md"));
console.log(await home.glob("/tester/**/*.md"));

const snap = await home.snapshot("initial write");
console.log(snap.snapshotId);
```

The lower-level `workspace()` API is still available for advanced flows that need explicit workspace IDs, but the default SDK flow is now the implicit home slice with absolute paths.

## Tests

```bash
npm test
```
