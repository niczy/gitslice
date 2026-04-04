import { getConfiguredAPIBaseURL } from '../shared/runtime.js';

function getGatewayTarget() {
  return getConfiguredAPIBaseURL(process.env, 'http://localhost:50051');
}

function buildProxyURL(request, suffix = '') {
  const url = new URL(request.url);
  const pathname = suffix.startsWith('/') ? suffix : `/${suffix}`;
  return new URL(`${pathname}${url.search}`, getGatewayTarget());
}

export async function proxyRequest(request, suffix = '') {
  const targetURL = buildProxyURL(request, suffix);
  const headers = new Headers(request.headers);
  headers.set('x-forwarded-host', new URL(request.url).host);
  headers.set('x-forwarded-proto', new URL(request.url).protocol.replace(':', ''));

  try {
    return await fetch(targetURL, {
      method: request.method,
      headers,
      body: ['GET', 'HEAD'].includes(request.method.toUpperCase()) ? undefined : await request.clone().arrayBuffer(),
      redirect: 'manual',
    });
  } catch {
    return Response.json({ error: 'Unable to reach upstream API origin' }, { status: 502 });
  }
}
