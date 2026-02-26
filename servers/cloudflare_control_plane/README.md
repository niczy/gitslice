# Cloudflare Runtime Control Plane

This package provides the Cloudflare Worker + Durable Object control plane used by gitslice runtime sessions for the `cloudflare_containers` backend.

## Endpoints

- `GET /internal/runtime/health`
- `POST /internal/runtime/sessions`
- `POST /internal/runtime/sessions/{id}/input`
- `POST /internal/runtime/sessions/{id}/interrupt`
- `DELETE /internal/runtime/sessions/{id}`
- `GET /internal/runtime/sessions/{id}/health`
- `GET /internal/runtime/sessions/{id}/stream`

All endpoints require service-token headers:

- `CF-Access-Client-Id`
- `CF-Access-Client-Secret`

Worker environment variables:

- `CFC_CONTROL_CLIENT_ID`
- `CFC_CONTROL_CLIENT_SECRET`
- `CFC_EVENT_BUFFER_SIZE` (optional, default `250`)

## Local development

```bash
cd servers/cloudflare_control_plane
npm install
npm test
npx wrangler dev
```

## Deploy

```bash
cd servers/cloudflare_control_plane
npx wrangler deploy
```
