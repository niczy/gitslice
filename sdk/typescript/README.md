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

const ws = await client.workspace("demo-sdk-workspace");
await ws.write("README.md", "hello from typescript\n");
console.log(await ws.read("README.md"));
console.log(await ws.glob("**/*.md"));

const snap = await ws.snapshot("initial write");
console.log(snap.snapshotId);
```

## Tests

```bash
npm test
```
