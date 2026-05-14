import { getConfiguredAPIBaseURL } from '../shared/runtime.js';
import { clearLocalSessionCookie, getAuthProvider, getClerkAdminClaimsResult, getProxyAuthorizationResult } from './auth.js';

function getGatewayTarget() {
  return getConfiguredAPIBaseURL(process.env, 'http://localhost:50051');
}

function buildProxyURL(request, suffix = '') {
  const url = new URL(request.url);
  const pathname = suffix.startsWith('/') ? suffix : `/${suffix}`;
  return new URL(`${pathname}${url.search}`, getGatewayTarget());
}

function isRestrictedAdminProxyPath(pathname) {
  const path = String(pathname || '').trim();
  return path === '/v1/admin/status'
    || path === '/v1/admin/users:deleteByEmail'
    || path.startsWith('/v1/admin/users/')
    || path === '/v1/admin/home-slices:backfill';
}

function appendResponseCookies(response, cookies = []) {
  for (const cookie of cookies) {
    if (cookie) {
      response.headers.append('Set-Cookie', cookie);
    }
  }
  return response;
}

export async function proxyRequest(request, suffix = '', options = {}) {
  const targetURL = buildProxyURL(request, suffix);
  const headers = new Headers(request.headers);
  headers.set('x-forwarded-host', new URL(request.url).host);
  headers.set('x-forwarded-proto', new URL(request.url).protocol.replace(':', ''));
  const responseCookies = [];
  const restrictedAdminPath = isRestrictedAdminProxyPath(targetURL.pathname);
  if (restrictedAdminPath && getAuthProvider() === 'clerk') {
    const authResult = await getClerkAdminClaimsResult(request, options);
    if (authResult.signedClaims) {
      headers.set('X-Gitslice-Clerk-Admin-Claims', authResult.signedClaims);
      headers.delete('Authorization');
    } else if (authResult.rejectUnauthenticated) {
      return Response.json({ error: 'Not signed in' }, { status: 401 });
    }
  }
  const requestAuthorization = String(headers.get('Authorization') || '').trim();
  const hasClerkBearerAuthorization = /^Bearer\s+/i.test(requestAuthorization);
  const shouldResolveClerkAuthorization = !requestAuthorization || hasClerkBearerAuthorization;
  if (!restrictedAdminPath && shouldResolveClerkAuthorization && getAuthProvider() === 'clerk') {
    const authResult = await getProxyAuthorizationResult(request, options);
    responseCookies.push(...(authResult.setCookies || []));
    if (authResult.authorization) {
      headers.set('Authorization', authResult.authorization);
    } else if (hasClerkBearerAuthorization || authResult.rejectUnauthenticated) {
      headers.delete('Authorization');
      return appendResponseCookies(Response.json({ error: 'Not signed in' }, { status: 401 }), responseCookies);
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
    if (response.status === 401) {
      const responseText = await response.clone().text();
      if (/invalid session token/i.test(responseText)) {
        const staleSessionHeaders = new Headers(response.headers);
        for (const cookie of responseCookies) {
          if (cookie) {
            staleSessionHeaders.append('Set-Cookie', cookie);
          }
        }
        staleSessionHeaders.append('Set-Cookie', clearLocalSessionCookie(request));
        return new Response(responseText, {
          status: response.status,
          statusText: response.statusText,
          headers: staleSessionHeaders,
        });
      }
    }
    return appendResponseCookies(new Response(response.body, response), responseCookies);
  } catch {
    return Response.json({ error: 'Unable to reach upstream API origin' }, { status: 502 });
  }
}
