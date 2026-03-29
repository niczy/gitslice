# Gitslice MCP Server

MCP stdio server for the Gitslice remote filesystem API.

## Install

```bash
cd sdk/mcp
npm install
```

## Run

```bash
GITSLICE_BASE_URL=https://agenttools.dev \
GITSLICE_USERNAME=tester \
GITSLICE_WORKSPACE=my-workspace \
npx gitslice-mcp
```

## Environment

- `GITSLICE_BASE_URL`: API base URL. Defaults to `http://127.0.0.1:50051`.
- `GITSLICE_USERNAME`: fake-user auth username for current repo auth.
- `GITSLICE_API_KEY`: optional bearer-style auth instead of username.
- `GITSLICE_WORKSPACE`: default workspace used by resources and any tool call that omits `workspaceId`.

## Tests

```bash
npm test
```
