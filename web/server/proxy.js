const gatewayTarget = process.env.VITE_FILE_API_PROXY_TARGET || 'http://localhost:8080';

function buildProxyURL(request, suffix = '') {
  const url = new URL(request.url);
  const pathname = suffix.startsWith('/') ? suffix : `/${suffix}`;
  return new URL(`${pathname}${url.search}`, gatewayTarget);
}

export async function proxyRequest(request, suffix = '') {
  const targetURL = buildProxyURL(request, suffix);
  const headers = new Headers(request.headers);
  headers.set('x-forwarded-host', new URL(request.url).host);
  headers.set('x-forwarded-proto', new URL(request.url).protocol.replace(':', ''));

  return fetch(targetURL, {
    method: request.method,
    headers,
    body: ['GET', 'HEAD'].includes(request.method.toUpperCase()) ? undefined : await request.clone().arrayBuffer(),
    redirect: 'manual',
  });
}
