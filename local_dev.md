# Local Development Notes

## Server restart learnings (2026-02-14)

- Do not trust restart script success output alone; verify services stay up after the command exits.
- After any restart, validate all expected listeners are present:
  - `127.0.0.1:50051` (production core gRPC + HTTP gateway)
  - `127.0.0.1:50052` (staging core gRPC + HTTP gateway, when staging env is configured)
- Verify both local and public health:
  - local production: `http://127.0.0.1:50051/health`
  - local staging: `http://127.0.0.1:50052/health`
  - public production: `https://gitslice.io/` and `https://api.gitslice.io/v1/global/state`
  - public staging: `https://agenttools.dev/` and `https://api.agenttools.dev/v1/global/state`
- The VM no longer serves the public web app. `gitslice.io` and `agenttools.dev` are Cloudflare Worker deployments, so a healthy API with a broken web page usually points to Worker deploy/config issues, not PM2.
- If Nginx is up but API hosts appear down, check the core listeners first before blaming proxy config.
- Keep core services supervised by PM2 for persistence and confirm with `pm2 ls`.
