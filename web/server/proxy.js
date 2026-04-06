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
  let responseCookie = '';
  let clearCookie = false;
  const hasWorkOSSessionCookie = String(request.headers.get('cookie') || '').includes('gs_workos_session=');
  if (!headers.has('Authorization') && getAuthProvider() === 'workos') {
    const authResult = await getProxyAuthorizationResult(request);
    responseCookie = authResult.setCookie || '';
    clearCookie = Boolean(authResult.clearCookie);
    if (authResult.authorization) {
      headers.set('Authorization', authResult.authorization);
    } else if (hasWorkOSSessionCookie && clearCookie) {
      const response = Response.json({ error: 'Not signed in' }, { status: 401 });
      response.headers.append('Set-Cookie', 'gs_workos_session=; Path=/; HttpOnly; SameSite=Lax; Max-Age=0; Secure');
      return response;
    }
  }

  try {
    const response = await fetch(targetURL, {
      method: request.method,
      headers,
      body: ['GET', 'HEAD'].includes(request.method.toUpperCase()) ? undefined : await request.clone().arrayBuffer(),
      redirect: 'manual',
    });
    const proxied = new Response(response.body, response);
    if (responseCookie) {
      proxied.headers.append('Set-Cookie', responseCookie);
    }
    if (clearCookie) {
      proxied.headers.append('Set-Cookie', 'gs_workos_session=; Path=/; HttpOnly; SameSite=Lax; Max-Age=0; Secure');
    }
    return proxied;
  } catch {
    return Response.json({ error: 'Unable to reach upstream API origin' }, { status: 502 });
  }
}
