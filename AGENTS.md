# Agent Instructions

These guidelines apply to the entire repository.

- Before any development work, read `local_dev.md` and follow its operational notes.
- Run `gofmt` on any modified Go files before committing.
- Run `make test` when changing Go code or protos to catch regressions (this installs dependencies).
- If `.proto` files are updated, regenerate the Go stubs with the commands in `README.md`, but do not commit generated `*.pb.go` / `*.pb.gw.go` files.
- Keep documentation changes concise and prefer updating existing sections instead of adding new top-level files unless necessary.
- APIs should be implemented gRPC-first: define/extend `.proto` services and expose HTTP via grpc-gateway bindings. Avoid adding standalone net/http REST endpoints for `/v1/*` when the route can be served through gateway.
- Agent session APIs must be exposed via gRPC service definitions with grpc-gateway HTTP bindings (no standalone net/http REST handlers for `/v1/agent-sessions`).
- Do not commit generated protobuf outputs (`*.pb.go`, `*.pb.gw.go`); generate them locally as part of build/test workflows.
- Keep the integration test (`workflow_test/integration_test.go`) exercising the CLI and services end to end; ensure it stays up to date when altering related behavior and run it with `RUN_INTEGRATION_TESTS=1 make test` during relevant changes.
- For local dev server starts:
  - Use `make restart-servers-postgres` for normal terminal sessions; it runs `dev/dev-servers.sh`, sources `web/.dev.vars`, defaults to local Postgres `gitslice_dev`, and leaves genesis population enabled for test data.
  - When starting servers from Codex/tool shells, verify that background processes survive after the command exits. If they do not, use a persistent supervisor such as `launchctl` and make the wrapped shell source `web/.dev.vars` before starting both `core_server` and the web dev server.
  - Local Clerk auth requires non-empty `AUTH_PROVIDER=clerk`, `CLERK_SECRET_KEY`, and either `CLERK_PUBLISHABLE_KEY` or `VITE_CLERK_PUBLISHABLE_KEY` in `web/.dev.vars`; after restart, check `http://localhost:5173/sign-in` does not show "Clerk is not fully configured."
  - After any local restart, verify `make dev-status`, `curl -sf http://localhost:50051/health`, and `curl -sfI http://localhost:5173/`.
- For deployment changes, keep `ops/restart_all.sh`, `ops/start_web_server.sh`, and crontab assumptions consistent:
  - `ops/restart_all.sh` must remain safe for unattended hourly runs.
  - Avoid changes that break `git pull --ff-only` based update flow.
  - Preserve lock/health-check behavior so cron runs do not overlap or silently fail.
  - Prefer running restarts via `ops/restart_all.sh` (it prepares `PATH`); running `ops/start_web_server.sh` directly with a missing `PATH` can fail `make build-core` (e.g. `protoc` not found) and leave Nginx upstreams down (resulting in `502`).
  - When diagnosing `502` on `agenttools.dev`, check the Nginx upstreams are actually listening: `127.0.0.1:4173` (web preview) and `127.0.0.1:50051` (core gRPC + HTTP gateway).
  - If PM2 logs show `listen tcp :50051: bind: address already in use`, there are multiple `core_server` instances running; stop the stray one or PM2 will flap/restart and admin operations (like git import) may hit the wrong instance.
  - If `/v1/global/state` returns `globalCommitHash=global-init` and empty history, genesis population/import hasn't run (or state was reset) for the currently running server.
  - Prod defaults: start `core_server` with `SKIP_GIT_POPULATION=1` (disable genesis auto-population from the local git checkout). For `STORAGE_TYPE=postgres`, ensure the object store is configured (GCS creds or `OBJECT_STORE_TYPE=filesystem` + `OBJECT_STORE_DIR`).
- If process supervision behavior changes, update `ops/ecosystem.config.cjs` and document PM2 usage in `README.md`.
- If reverse proxy behavior changes, update `ops/nginx.conf` and the related Nginx section in `README.md` in the same PR.

## Gitslice CLI Primer

`gs` is this repo's project CLI. Do not assume a Gitslice checkout behaves like
a normal git checkout; prefer `gs` commands for slice state, diffs, sync, export,
and publish workflows.

When entering an unknown workspace, start with:

```bash
gs context --json
```

Use that output to identify the current endpoint, user, slice binding, checkout
metadata, and changeset context before making changes.

Core concepts:

- A **slice** is a focused workspace over a subset of files.
- A **slice checkout** is a local materialized copy of a slice and contains
  `.gs/` metadata. Do not delete or rewrite `.gs/`.
- A **changeset** records local checkout changes for export or publish.
- An **agent runner** is a local process started with `gs agent run` or
  `gs agent start`; it registers with the server and provides an environment for
  Codex, Claude, or a custom command.
- An **agent session** is one conversation/task assigned to a runner. A runner
  can handle multiple sessions, and each session should use its own slice
  checkout.

Common slice commands:

```bash
gs slice list --json
gs slice checkout <slice-id-or-ref> --json
gs slice checkout <slice-id-or-ref> --here --json
gs slice status --json
gs slice diff --summary
gs slice diff --stat
gs slice search <query> --json
gs slice sync --json
gs slice export --json
gs slice publish --json
```

Use `gs slice sync` only when the checkout is clean. If it is dirty, inspect
with `gs slice diff` first. Use `gs slice export --json` to create or update a
changeset without merging; use `gs slice publish --json` only when the user has
asked to publish or merge the changes.

Local agent runner commands:

```bash
gs agent run --dir ~/gitslice-agents --agent codex
gs agent start --dir ~/gitslice-agents --agent codex --json
gs agent run --dir ~/gitslice-agents --agent claude
gs agent input <session-id> "message"
gs agent interrupt <session-id> --reason "user interrupt"
gs agent stop <session-id>
```

Safety rules for agents:

- Prefer `gs context --json` before assuming auth, endpoint, or slice state.
- Prefer `gs slice diff` over `git diff` inside slice checkouts.
- Do not remove `.gs/` metadata from a slice checkout.
- Do not run `gs slice publish` unless the user asked to publish or merge.
- If auth fails, ask the user or run `gs login`; do not manually edit stored
  credentials.

## GitHub Workflow

**Always create a Pull Request before merging to main:**

1. **Create a feature branch** for your changes:
   ```bash
   git checkout -b feature/your-feature-name
   ```

2. **Make your changes** and commit them:
   ```bash
   git add -A
   git commit -m "feat: description of your changes"
   ```

3. **Push your branch** and create a PR:
   ```bash
   git push -u origin feature/your-feature-name
   gh pr create --title "Your PR title" --body "PR description"
   ```

4. **Check GitHub Actions status and fix any issues:**
   ```bash
   gh run list --limit 3
   ```
   - If a workflow fails, view details with `gh run view <run-number>`
   - Check failed logs with `gh run view <run-number> --log-failed`
   - Fix the issues, commit, and push again
   - Only proceed to merge when all checks pass

5. **Address PR review comments:**
   - Check for comments with `gh pr view <pr-number> --comments`
   - Address each comment and reply or react appropriately
   - Push additional fixes if needed
   - Re-run checks after pushing fixes

6. **Merge the PR** only after all checks pass:
   ```bash
   gh pr merge <pr-number> --admin --merge
   ```
   Or merge via the GitHub web UI after confirming all tests pass.

7. **Deploy merged main to staging and verify it:**
   ```bash
   git checkout main
   git pull --ff-only
   ./ops/deploy.sh --env staging --app all
   ```
   - Use the staging env file selected by `ops/deploy.sh` (`ops/.env.staging`).
   - Treat the deploy as incomplete until the script reports deployment verification passed.
   - After deploy, verify the public staging web and API endpoints:
     ```bash
     curl -sfI https://agenttools.dev/
     curl -sf https://api.agenttools.dev/v1/global/state
     ```
   - If verification fails, diagnose and fix forward with a new PR instead of manually patching `main`.

**Never push directly to main** - always use the PR workflow to ensure tests run before merging.

## Quick Reference

| Action | Command |
|--------|---------|
| Create feature branch | `git checkout -b feature/name` |
| Commit changes | `git add -A && git commit -m "feat: description"` |
| Push branch | `git push -u origin feature/name` |
| Create PR | `gh pr create --title "title" --body "body"` |
| Check PR status | `gh pr view <pr-number>` |
| Check Actions | `gh run list` then `gh run view <run-number> --log-failed` if failed |
| Address comments | `gh pr view <pr-number> --comments` then fix and push |
| Fix failed Actions | Fix issues, commit, and `git push` |
| Merge PR | `gh pr merge <pr-number> --admin --merge` |
| Update local main | `git checkout main && git pull --ff-only` |
| Deploy staging | `./ops/deploy.sh --env staging --app all` |
| Verify staging | `curl -sfI https://agenttools.dev/ && curl -sf https://api.agenttools.dev/v1/global/state` |
