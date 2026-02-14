# Local Development Notes

## Server restart learnings (2026-02-14)

- Do not trust restart script success output alone; verify services stay up after the command exits.
- After any restart, validate all expected listeners are present:
  - `127.0.0.1:4173` (web preview)
  - `127.0.0.1:8080` (gateway)
  - `:50051` (core gRPC)
- Verify both local and public health:
  - local: `http://127.0.0.1:8080/health`
  - public: `https://agenttools.dev/` and `https://agenttools.dev/v1/global/state`
- If Nginx is up but app/API appear down, check upstream listeners first before blaming proxy config.
- Keep services supervised by PM2 for persistence and confirm with `pm2 ls`.
