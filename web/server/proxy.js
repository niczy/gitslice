import { getConfiguredAPIBaseURL } from '../shared/runtime.js';
import { getAuthProvider, getProxyAuthorizationResult } from './auth.js';

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
  const responseCookies = [];
  if (!headers.has('Authorization') && ['workos', 'clerk'].includes(getAuthProvider())) {
    const authResult = await getProxyAuthorizationResult(request);
    responseCookies.push(...(authResult.setCookies || []));
    if (authResult.authorization) {
      headers.set('Authorization', authResult.authorization);
    } else if (authResult.rejectUnauthenticated) {
      const response = Response.json({ error: 'Not signed in' }, { status: 401 });
      for (const cookie of responseCookies) {
        if (cookie) {
          response.headers.append('Set-Cookie', cookie);
        }
      }
      return response;
    }
  }

  try {
    const body = ['GET', 'HEAD'].includes(request.method.toUpperCase()) ? undefined : await request.clone().arrayBuffer();
    const response = await fetch(targetURL, {
      method: request.method,
      headers,
      body,
      redirect: 'manual',
    });
    const proxied = new Response(response.body, response);
    for (const cookie of responseCookies) {
      if (cookie) {
        proxied.headers.append('Set-Cookie', cookie);
      }
    }
    return proxied;
  } catch {
    return Response.json({ error: 'Unable to reach upstream API origin' }, { status: 502 });
  }
}
