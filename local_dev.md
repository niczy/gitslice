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

## Getting authenticated for CLI and API testing

### CLI binary

The `gs` binary is installed at `~/go/bin/gs`. Build it with:

```bash
go install ./gs/...
```

The `./gs` path in the repo root is a Go package directory, not the binary.

### CLI endpoint configuration

```bash
# Staging
gs config endpoint set api.agenttools.dev:443 --tls --json

# Local staging (no TLS, if server running on the VM)
gs config endpoint set 127.0.0.1:50052 --json

# Production
gs config endpoint set api.gitslice.io:443 --tls --json
```

### Getting a fresh access token (programmatic, no browser)

Staging uses `AUTH_PROVIDER=clerk` with a bridge token signed by `AUTH_SECRET`.
Generate a signed claim and get back an access + refresh token:

```python
import json, base64, hmac, hashlib, time

AUTH_SECRET = "2e4d997c2d26fdf16b9e618e99f94e188fbc7a6b583f47ddc0ff081984755a04"
now_ms = int(time.time() * 1000)

claims = {
    "provider": "clerk",
    "userId": "user_test_...",       # unique Clerk user ID
    "sessionId": "sess_test_...",    # unique session ID
    "email": "test@example.com",     # unique email
    "name": "Test User",
    "preferredUsername": "testuser",
    "imageUrl": "",
    "issuedAtMs": now_ms,
    "expiresAtMs": now_ms + 600000   # max 15 min (bridgeTokenMaxLifetime)
}

payload = base64.urlsafe_b64encode(json.dumps(claims, separators=(',', ':')).encode()).decode().rstrip('=')
mac = hmac.new(AUTH_SECRET.encode(), payload.encode(), hashlib.sha256)
signature = base64.urlsafe_b64encode(mac.digest()).decode().rstrip('=')
signed_claims = f"{payload}.{signature}"
```

Then exchange it:

```bash
curl -s -X POST https://api.agenttools.dev/v1/auth/clerk/ensure-local-identity \
  -H "Content-Type: application/json" \
  -d "{\"signedClaims\":\"$SIGNED_CLAIMS\",\"issueLocalSession\":true}"
```

The response contains `accessToken` and `refreshToken`. Use `accessToken` as `Authorization: Bearer <token>`.

### Refreshing an existing token (no browser)

If you have a valid refresh token (stored in the `auth_sessions` table or from a prior login):

```bash
curl -s -X POST https://api.agenttools.dev/v1/auth/token/refresh \
  -H "Content-Type: application/json" \
  -d '{"refreshToken":"gsr_..."}'
```

Access tokens expire in 15 minutes. Refresh tokens last 30 days.

### Common API patterns for testing

```bash
TOKEN="gs_..."

# List slices (includes folder_mounts since the new proto field)
curl -s -H "Authorization: Bearer $TOKEN" https://api.agenttools.dev/v1/slices

# Add tracked folder to custom slice
curl -s -X POST "https://api.agenttools.dev/v1/slices/<slice_id>/folders:add" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"folderPath":"nicholas/test-project"}'

# Remove tracked folder (supports both source_path and alias)
curl -s -X POST "https://api.agenttools.dev/v1/slices/<slice_id>/folders:remove" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"folderPath":"test-project"}'

# Create directory (path must be absolute, URL-encoded)
curl -s -X POST "https://api.agenttools.dev/v1/fs/workspaces/<ws>/mkdir/%2Fpath%2Fto%2Fdir" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" -d '{}'

# Write file (PUT, not POST; path absolute)
curl -s -X PUT "https://api.agenttools.dev/v1/fs/workspaces/<ws>/files/%2Fpath%2Fto%2Ffile" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"content":"SGVsbG8=","base64":true}'
```
