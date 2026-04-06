import { Auth } from '@auth/core';
import GitHub from '@auth/core/providers/github';
import Google from '@auth/core/providers/google';
import { WorkOS } from '@workos-inc/node';
import {
  decodeBase64URLUTF8,
  encodeBase64URLUTF8,
  getConfiguredAPIBaseURL,
  randomHex,
  signHMACSHA256Base64URL,
  timingSafeEqualText,
} from '../shared/runtime.js';

const DEV_SESSION_COOKIE = 'gs_dev_session';
const WORKOS_SESSION_COOKIE = 'gs_workos_session';
const WORKOS_STATE_COOKIE = 'gs_workos_state';
const WORKOS_STATE_COOKIE_MAX_AGE = 60 * 10;
const WORKOS_SESSION_COOKIE_MAX_AGE = 60 * 60 * 24 * 30;
const USERNAME_PATTERN = /^[A-Za-z0-9][A-Za-z0-9_-]{2,31}$/;

function getGatewayTarget() {
  return getConfiguredAPIBaseURL(process.env, 'http://localhost:50051');
}

export function getAuthProvider() {
  return String(process.env.AUTH_PROVIDER || 'local').trim().toLowerCase() || 'local';
}

function parseBooleanEnv(value) {
  const normalized = String(value || '').trim().toLowerCase();
  if (!normalized) {
    return null;
  }
  if (['1', 'true', 'yes', 'on'].includes(normalized)) {
    return true;
  }
  if (['0', 'false', 'no', 'off'].includes(normalized)) {
    return false;
  }
  return null;
}

function isLocalHost(urlString) {
  try {
    const url = new URL(urlString);
    const hostname = String(url.hostname || '').trim().toLowerCase();
    return hostname === 'localhost' || hostname === '127.0.0.1' || hostname === '::1';
  } catch {
    return false;
  }
}

export function isDevLoginEnabled(request, authContext = null) {
  const explicit = parseBooleanEnv(process.env.ALLOW_DEV_LOGIN);
  if (explicit !== null) {
    return explicit;
  }
  const provider = String(authContext?.authProvider || getAuthProvider()).trim().toLowerCase();
  if (provider !== 'workos') {
    return true;
  }
  return isLocalHost(request?.url);
}

export function getPublicAuthConfig(request) {
  const authContext = createAuthContext(request);
  return {
    authProvider: String(authContext?.authProvider || getAuthProvider()).trim().toLowerCase() || 'local',
    allowDevLogin: isDevLoginEnabled(request, authContext),
  };
}

function buildUsernameFromProfile(profile) {
  const raw = [
    profile?.preferred_username,
    profile?.login,
    profile?.name,
    profile?.email?.split('@')?.[0],
    profile?.sub,
    profile?.id,
  ].find(Boolean);

  const normalized = String(raw || 'user')
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, '-')
    .replace(/^-+|-+$/g, '');

  const safe = normalized || 'user';
  const startsWithAlnum = /^[a-z0-9]/.test(safe) ? safe : `u-${safe}`;
  const trimmed = startsWithAlnum.slice(0, 32).replace(/-+$/g, '');
  if (trimmed.length >= 3) {
    return trimmed;
  }
  return `${trimmed}${randomHex(2)}`.slice(0, 8);
}

function getWorkOSRedirectURI(request) {
  const configured = String(process.env.WORKOS_REDIRECT_URI || '').trim();
  if (configured) {
    return configured;
  }
  if (!request) {
    return '';
  }
  return new URL('/auth/callback/workos', request.url).toString();
}

function buildCookieSessionDisplayName(user) {
  const fullName = [user?.firstName, user?.lastName].filter(Boolean).join(' ').trim();
  return fullName || String(user?.email || user?.id || '').trim();
}

async function signPayload(payload, authSecret) {
  const encoded = encodeBase64URL(JSON.stringify(payload));
  const signature = await signHMACSHA256Base64URL(authSecret, encoded);
  return `${encoded}.${signature}`;
}

async function verifySignedPayload(rawValue, authSecret) {
  if (!rawValue || !authSecret) {
    return null;
  }
  const [payload, signature] = String(rawValue).split('.');
  if (!payload || !signature) {
    return null;
  }
  const expected = await signHMACSHA256Base64URL(authSecret, payload);
  if (!timingSafeEqualText(signature, expected)) {
    return null;
  }
  try {
    return JSON.parse(decodeBase64URL(payload));
  } catch {
    return null;
  }
}

function normalizeReturnTo(request, rawValue, fallbackPath = '/login') {
  const fallbackURL = new URL(fallbackPath, request.url);
  const candidate = String(rawValue || '').trim();
  if (!candidate) {
    return fallbackURL.toString();
  }
  try {
    const resolved = new URL(candidate, request.url);
    if (resolved.origin !== fallbackURL.origin) {
      return fallbackURL.toString();
    }
    return resolved.toString();
  } catch {
    return fallbackURL.toString();
  }
}

function buildWorkOSSessionPayload(authResponse) {
  const user = authResponse?.user;
  if (!user?.id) {
    return null;
  }
  return {
    user: {
      id: user.id,
      email: user.email,
      name: buildCookieSessionDisplayName(user),
      username: '',
      workosUserId: user.id,
      derivedUsername: buildUsernameFromProfile({
        id: user.id,
        email: user.email,
        name: buildCookieSessionDisplayName(user),
      }),
      profilePictureUrl: user.profilePictureUrl || '',
    },
    source: 'workos',
    sessionId: authResponse.sessionId || '',
    organizationId: authResponse.organizationId || '',
    authenticationMethod: authResponse.authenticationMethod || '',
  };
}

function createLocalAuthContext(authSecret) {
  const providers = [];
  if (process.env.AUTH_GOOGLE_ID && process.env.AUTH_GOOGLE_SECRET) {
    providers.push(
      Google({
        clientId: process.env.AUTH_GOOGLE_ID,
        clientSecret: process.env.AUTH_GOOGLE_SECRET,
      }),
    );
  }
  if (process.env.AUTH_GITHUB_ID && process.env.AUTH_GITHUB_SECRET) {
    providers.push(
      GitHub({
        clientId: process.env.AUTH_GITHUB_ID,
        clientSecret: process.env.AUTH_GITHUB_SECRET,
      }),
    );
  }

  return {
    authProvider: 'local',
    startupError: '',
    authSecret,
    authConfig: {
      trustHost: true,
      secret: authSecret,
      basePath: '/auth',
      session: { strategy: 'jwt' },
      providers,
      callbacks: {
        async jwt({ token, profile }) {
          if (profile) {
            token.username = buildUsernameFromProfile(profile);
          }
          return token;
        },
        async session({ session, token }) {
          if (!session.user) {
            session.user = {};
          }
          session.user.username = token.username;
          return session;
        },
      },
    },
  };
}

function createWorkOSAuthContext(authSecret, request) {
  const clientId = String(process.env.WORKOS_CLIENT_ID || '').trim();
  const apiKey = String(process.env.WORKOS_API_KEY || '').trim();
  const redirectURI = getWorkOSRedirectURI(request);
  const cookiePassword = String(process.env.WORKOS_COOKIE_PASSWORD || '').trim();
  if (!clientId || !apiKey || !redirectURI || !cookiePassword) {
    return {
      authProvider: 'workos',
      startupError: 'WorkOS auth is not fully configured',
      authSecret,
    };
  }
  return {
    authProvider: 'workos',
    startupError: '',
    authSecret,
    workos: new WorkOS(apiKey, { clientId }),
    workosClientId: clientId,
    workosRedirectURI: redirectURI,
    workosCookiePassword: cookiePassword,
    workosAuthKitDomain: String(process.env.WORKOS_AUTHKIT_DOMAIN || '').trim(),
  };
}

export function createAuthContext(request) {
  const authSecret = process.env.AUTH_SECRET;
  if (!authSecret) {
    return { authProvider: getAuthProvider(), startupError: 'AUTH_SECRET is not configured' };
  }
  if (getAuthProvider() === 'workos') {
    return createWorkOSAuthContext(authSecret, request);
  }
  return createLocalAuthContext(authSecret);
}

function encodeBase64URL(value) {
  return encodeBase64URLUTF8(value);
}

function decodeBase64URL(value) {
  return decodeBase64URLUTF8(value);
}

async function signDevSession(username, authSecret) {
  return signPayload({ username }, authSecret);
}

async function verifyDevSession(rawValue, authSecret) {
  const parsed = await verifySignedPayload(rawValue, authSecret);
  const username = String(parsed?.username || '').trim();
  return USERNAME_PATTERN.test(username) ? username : '';
}

function escapeHTML(value) {
  return String(value || '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;');
}

function serializeCookie(request, name, value, maxAgeSeconds) {
  const url = new URL(request.url);
  const proto = String(request.headers.get('x-forwarded-proto') || '').trim();
  const secure = proto === 'https' || url.protocol === 'https:';
  const parts = [`${name}=${value}`, 'Path=/', 'HttpOnly', 'SameSite=Lax'];
  if (typeof maxAgeSeconds === 'number') {
    parts.push(`Max-Age=${maxAgeSeconds}`);
  }
  if (secure) {
    parts.push('Secure');
  }
  return parts.join('; ');
}

function parseCookieHeader(value) {
  const cookies = new Map();
  for (const chunk of String(value || '').split(';')) {
    const trimmed = chunk.trim();
    if (!trimmed) {
      continue;
    }
    const separator = trimmed.indexOf('=');
    if (separator < 0) {
      continue;
    }
    cookies.set(trimmed.slice(0, separator).trim(), trimmed.slice(separator + 1).trim());
  }
  return cookies;
}

function redirectWithCookies(location, cookies = []) {
  const headers = new Headers({ Location: location });
  for (const cookie of cookies) {
    if (cookie) {
      headers.append('Set-Cookie', cookie);
    }
  }
  return new Response(null, { status: 302, headers });
}

export async function loadAuthJsSession(request, authConfig) {
  if (!authConfig) {
    return null;
  }
  const sessionURL = new URL('/auth/session', request.url);
  const response = await Auth(
    new Request(sessionURL, {
      method: 'GET',
      headers: request.headers,
    }),
    authConfig,
  );
  if (!response.ok) {
    return null;
  }
  return response.json();
}

async function authenticateWorkOSSession(request, authContext) {
  if (!authContext?.workos || !authContext?.workosCookiePassword) {
    return { session: null, accessToken: '', sessionId: '', clearCookie: false };
  }
  const sealedSession = parseCookieHeader(request.headers.get('cookie')).get(WORKOS_SESSION_COOKIE);
  if (!sealedSession) {
    return { session: null, accessToken: '', sessionId: '', clearCookie: false };
  }

  try {
    const authResponse = await authContext.workos.userManagement.authenticateWithSessionCookie({
      sessionData: sealedSession,
      cookiePassword: authContext.workosCookiePassword,
    });
    if (!authResponse.authenticated) {
      return {
        session: null,
        accessToken: '',
        sessionId: '',
        clearCookie: authResponse.reason !== 'no_session_cookie_provided',
      };
    }
    return {
      session: buildWorkOSSessionPayload(authResponse),
      accessToken: authResponse.accessToken,
      sessionId: authResponse.sessionId || '',
      clearCookie: false,
    };
  } catch {
    return { session: null, accessToken: '', sessionId: '', clearCookie: true };
  }
}

async function ensureWorkOSLocalIdentity(request, accessToken, session) {
  if (!session?.user?.workosUserId || !accessToken) {
    return session;
  }

  const response = await fetch(new URL('/v1/auth/workos/ensure-local-identity', getGatewayTarget()), {
    method: 'POST',
    headers: {
      Accept: 'application/json',
      Authorization: `Bearer ${accessToken}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      preferredUsername: session.user.derivedUsername || '',
      name: session.user.name || '',
      primaryEmail: session.user.email || '',
    }),
  });

  if (!response.ok) {
    let detail = '';
    try {
      const payload = await response.json();
      detail = String(payload?.message || payload?.error || '').trim();
    } catch {
      detail = '';
    }
    throw new Error(detail || `failed to ensure local WorkOS identity (${response.status})`);
  }

  const payload = await response.json();
  const localUsername = String(payload?.user?.username || '').trim();
  return {
    ...session,
    accountId: String(payload?.accountId || '').trim(),
    linkedExistingUser: Boolean(payload?.linkedExistingUser),
    user: {
      ...(session.user || {}),
      username: localUsername,
      localUsername,
      name: String(payload?.user?.name || session.user?.name || '').trim(),
      email: String(payload?.user?.primaryEmail || session.user?.email || '').trim(),
    },
  };
}

export async function getProxyAuthorizationHeader(request) {
  const authContext = createAuthContext(request);
  if (authContext.authProvider !== 'workos' || authContext.startupError) {
    return '';
  }
  const { accessToken } = await authenticateWorkOSSession(request, authContext);
  return accessToken ? `Bearer ${accessToken}` : '';
}

export async function loadSession(request) {
  const authContext = createAuthContext(request);
  if (!authContext?.authSecret) {
    return null;
  }
  const allowDevLogin = isDevLoginEnabled(request, authContext);
  if (authContext.authProvider === 'workos') {
    const { session, accessToken } = await authenticateWorkOSSession(request, authContext);
    if (!session) {
      if (!allowDevLogin) {
        return null;
      }
      const devUsername = await verifyDevSession(parseCookieHeader(request.headers.get('cookie')).get(DEV_SESSION_COOKIE), authContext.authSecret);
      if (!devUsername) {
        return null;
      }
      return {
        user: {
          name: devUsername,
          username: devUsername,
        },
        source: 'dev',
        expires: '',
      };
    }
    return ensureWorkOSLocalIdentity(request, accessToken, session);
  }

  const authSession = await loadAuthJsSession(request, authContext.authConfig);
  const oauthUsername = String(authSession?.user?.username || '').trim();
  if (oauthUsername) {
    return {
      ...authSession,
      source: 'oauth',
      user: {
        ...(authSession?.user || {}),
        username: oauthUsername,
      },
    };
  }

  const devUsername = await verifyDevSession(parseCookieHeader(request.headers.get('cookie')).get(DEV_SESSION_COOKIE), authContext.authSecret);
  if (!devUsername) {
    return null;
  }

  return {
    user: {
      name: devUsername,
      username: devUsername,
    },
    source: 'dev',
    expires: '',
  };
}

export async function handleSessionRequest(request) {
  const authContext = createAuthContext(request);
  const allowDevLogin = isDevLoginEnabled(request, authContext);
  if (authContext.startupError && !(allowDevLogin && authContext.authSecret)) {
    return Response.json({ error: authContext.startupError }, { status: 500 });
  }

  if (authContext.authProvider === 'workos') {
    let session = null;
    let clearCookie = false;
    if (!authContext.startupError) {
      try {
        const authSession = await authenticateWorkOSSession(request, authContext);
        clearCookie = authSession.clearCookie;
        if (authSession.session) {
          session = await ensureWorkOSLocalIdentity(request, authSession.accessToken, authSession.session);
        }
      } catch (error) {
        return Response.json({ error: error instanceof Error ? error.message : 'Failed to load WorkOS session' }, { status: 500 });
      }
    }

    if (!session && allowDevLogin) {
      const devUsername = await verifyDevSession(parseCookieHeader(request.headers.get('cookie')).get(DEV_SESSION_COOKIE), authContext.authSecret);
      if (devUsername) {
        session = {
          user: {
            name: devUsername,
            username: devUsername,
          },
          source: 'dev',
          expires: '',
        };
      }
    }

    const response = session ? Response.json(session) : Response.json({ error: 'Not signed in' }, { status: 401 });
    if (clearCookie) {
      response.headers.append('Set-Cookie', serializeCookie(request, WORKOS_SESSION_COOKIE, '', 0));
    }
    return response;
  }

  const session = await loadSession(request);
  if (!session?.user?.username) {
    return Response.json({ error: 'Not signed in' }, { status: 401 });
  }
  return Response.json(session);
}

export async function handleDevLoginRequest(request) {
  const authContext = createAuthContext(request);
  const { authSecret, startupError } = authContext;
  const allowDevLogin = isDevLoginEnabled(request, authContext);
  if ((!allowDevLogin && startupError) || !authSecret) {
    return Response.json({ error: startupError || 'Auth is not configured' }, { status: 500 });
  }
  if (!allowDevLogin) {
    return Response.json({ error: 'Development login is disabled' }, { status: 403 });
  }

  let payload = {};
  try {
    payload = await request.json();
  } catch {
    return Response.json({ error: 'Invalid JSON body' }, { status: 400 });
  }

  const username = String(payload?.username || '').trim();
  if (!USERNAME_PATTERN.test(username)) {
    return Response.json({ error: 'Invalid username' }, { status: 400 });
  }

  let loginResponse;
  try {
    loginResponse = await fetch(new URL('/v1/auth/login', getGatewayTarget()), {
      method: 'POST',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ username }),
    });
  } catch {
    return Response.json({ error: 'Unable to reach the API origin for development login' }, { status: 502 });
  }
  if (!loginResponse.ok) {
    const bodyText = await loginResponse.text();
    return new Response(bodyText || JSON.stringify({ error: 'Unable to provision development account' }), {
      status: loginResponse.status,
      headers: {
        'Content-Type': loginResponse.headers.get('content-type') || 'application/json',
      },
    });
  }

  return new Response(JSON.stringify({
    user: {
      name: username,
      username,
    },
    source: 'dev',
    expires: '',
  }), {
      status: 200,
      headers: {
        'Content-Type': 'application/json',
        'Set-Cookie': serializeCookie(request, DEV_SESSION_COOKIE, await signDevSession(username, authSecret), 60 * 60 * 24 * 30),
      },
    });
}

export function handleDevLogoutRequest(request) {
  return new Response(JSON.stringify({ ok: true }), {
    status: 200,
    headers: {
      'Content-Type': 'application/json',
      'Set-Cookie': serializeCookie(request, DEV_SESSION_COOKIE, '', 0),
    },
  });
}

async function handleWorkOSSignInRequest(request, authContext) {
  const url = new URL(request.url);
  const callbackURL = normalizeReturnTo(request, url.searchParams.get('callbackUrl'), '/login');
  const state = randomHex(16);
  const stateCookie = await signPayload({ state, callbackUrl: callbackURL }, authContext.authSecret);
  const redirectURL = authContext.workos.userManagement.getAuthorizationUrl({
    provider: 'authkit',
    clientId: authContext.workosClientId,
    redirectUri: authContext.workosRedirectURI,
    state,
  });
  return redirectWithCookies(redirectURL, [
    serializeCookie(request, WORKOS_STATE_COOKIE, stateCookie, WORKOS_STATE_COOKIE_MAX_AGE),
  ]);
}

async function handleWorkOSCallbackRequest(request, authContext) {
  const url = new URL(request.url);
  const signedState = parseCookieHeader(request.headers.get('cookie')).get(WORKOS_STATE_COOKIE);
  const statePayload = await verifySignedPayload(signedState, authContext.authSecret);
  const callbackURL = normalizeReturnTo(request, statePayload?.callbackUrl, '/login');
  const clearStateCookie = serializeCookie(request, WORKOS_STATE_COOKIE, '', 0);
  const errorCode = String(url.searchParams.get('error') || '').trim();
  const returnedState = String(url.searchParams.get('state') || '').trim();
  const code = String(url.searchParams.get('code') || '').trim();

  if (errorCode || !statePayload?.state || !returnedState || returnedState !== String(statePayload.state)) {
    const failureURL = new URL(callbackURL);
    failureURL.searchParams.set('error', errorCode || 'workos_callback_failed');
    return redirectWithCookies(failureURL.toString(), [clearStateCookie]);
  }
  if (!code) {
    const failureURL = new URL(callbackURL);
    failureURL.searchParams.set('error', 'workos_code_missing');
    return redirectWithCookies(failureURL.toString(), [clearStateCookie]);
  }

  let authResponse;
  try {
    authResponse = await authContext.workos.userManagement.authenticateWithCode({
      clientId: authContext.workosClientId,
      code,
      redirectUri: authContext.workosRedirectURI,
      ipAddress: String(request.headers.get('cf-connecting-ip') || '').trim(),
      userAgent: String(request.headers.get('user-agent') || '').trim(),
      session: {
        sealSession: true,
        cookiePassword: authContext.workosCookiePassword,
      },
    });
  } catch {
    const failureURL = new URL(callbackURL);
    failureURL.searchParams.set('error', 'workos_authenticate_failed');
    return redirectWithCookies(failureURL.toString(), [clearStateCookie]);
  }

  const sealedSession = String(authResponse?.sealedSession || '').trim();
  if (!sealedSession) {
    const failureURL = new URL(callbackURL);
    failureURL.searchParams.set('error', 'workos_session_missing');
    return redirectWithCookies(failureURL.toString(), [clearStateCookie]);
  }

  return redirectWithCookies(callbackURL, [
    clearStateCookie,
    serializeCookie(request, WORKOS_SESSION_COOKIE, sealedSession, WORKOS_SESSION_COOKIE_MAX_AGE),
  ]);
}

async function handleWorkOSLogoutRequest(request, authContext) {
  const returnTo = normalizeReturnTo(request, new URL(request.url).searchParams.get('callbackUrl'), '/');
  const sealedSession = parseCookieHeader(request.headers.get('cookie')).get(WORKOS_SESSION_COOKIE);
  let redirectURL = returnTo;
  if (sealedSession) {
    try {
      const cookieSession = authContext.workos.userManagement.loadSealedSession({
        sessionData: sealedSession,
        cookiePassword: authContext.workosCookiePassword,
      });
      redirectURL = await cookieSession.getLogoutUrl({ returnTo });
    } catch {
      redirectURL = returnTo;
    }
  }
  return redirectWithCookies(redirectURL, [
    serializeCookie(request, WORKOS_SESSION_COOKIE, '', 0),
  ]);
}

export async function handleAuthRequest(request) {
  const authContext = createAuthContext(request);
  if (authContext.startupError) {
    return Response.json({ error: authContext.startupError || 'Auth is not configured' }, { status: 500 });
  }
  if (authContext.authProvider === 'workos') {
    const pathname = new URL(request.url).pathname.replace(/^\/auth\/?/, '');
    if (pathname === 'signin/workos') {
      return handleWorkOSSignInRequest(request, authContext);
    }
    if (pathname === 'callback/workos') {
      return handleWorkOSCallbackRequest(request, authContext);
    }
    if (pathname === 'signout') {
      return handleWorkOSLogoutRequest(request, authContext);
    }
    return Response.json({ error: 'WorkOS auth route not found' }, { status: 404 });
  }
  if (!authContext.authConfig) {
    return Response.json({ error: 'Auth is not configured' }, { status: 500 });
  }
  return Auth(request, authContext.authConfig);
}

export function renderDevicePage({ userCode, startupError }) {
  const safeUserCode = escapeHTML(userCode);
  const safeStartupError = escapeHTML(startupError);
  return `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>Authorize Device</title>
    <style>
      :root {
        color-scheme: light;
        --bg: #f4f3ee;
        --panel: rgba(255, 255, 255, 0.92);
        --text: #1f1f1c;
        --muted: #5a5a54;
        --accent: #0f766e;
        --accent-dark: #115e59;
        --border: rgba(31, 31, 28, 0.12);
        --error: #b42318;
        --success: #027a48;
      }
      * { box-sizing: border-box; }
      body {
        margin: 0;
        min-height: 100vh;
        font-family: "Iowan Old Style", "Palatino Linotype", "Book Antiqua", serif;
        color: var(--text);
        background:
          radial-gradient(circle at top left, rgba(15, 118, 110, 0.12), transparent 32%),
          radial-gradient(circle at bottom right, rgba(15, 118, 110, 0.08), transparent 28%),
          linear-gradient(180deg, #faf8f2 0%, var(--bg) 100%);
        display: grid;
        place-items: center;
        padding: 24px;
      }
      main {
        width: min(100%, 560px);
        background: var(--panel);
        border: 1px solid var(--border);
        border-radius: 24px;
        padding: 32px;
        box-shadow: 0 18px 60px rgba(31, 31, 28, 0.10);
      }
      h1 {
        margin: 0 0 10px;
        font-size: clamp(2rem, 4vw, 2.5rem);
        line-height: 1;
      }
      p {
        margin: 0 0 14px;
        color: var(--muted);
        line-height: 1.5;
      }
      label {
        display: block;
        font-size: 0.92rem;
        font-weight: 700;
        margin: 24px 0 8px;
      }
      input {
        width: 100%;
        padding: 14px 16px;
        border-radius: 14px;
        border: 1px solid var(--border);
        font-size: 1.05rem;
        letter-spacing: 0.08em;
        text-transform: uppercase;
      }
      .row {
        display: flex;
        flex-wrap: wrap;
        gap: 12px;
        margin-top: 20px;
      }
      button, a.button {
        appearance: none;
        border: 0;
        border-radius: 999px;
        padding: 14px 18px;
        font-size: 0.98rem;
        font-weight: 700;
        text-decoration: none;
        cursor: pointer;
      }
      button.primary {
        background: var(--accent);
        color: white;
      }
      button.primary:hover {
        background: var(--accent-dark);
      }
      a.secondary {
        background: rgba(15, 118, 110, 0.08);
        color: var(--accent-dark);
      }
      #session-state {
        margin-top: 18px;
        padding: 14px 16px;
        border-radius: 14px;
        background: rgba(15, 118, 110, 0.06);
        color: var(--accent-dark);
      }
      #status {
        min-height: 1.5em;
        margin-top: 18px;
        font-weight: 700;
      }
      #status.error { color: var(--error); }
      #status.success { color: var(--success); }
      #sign-in {
        display: none;
      }
      body[data-startup-error="1"] #authorize {
        opacity: 0.6;
        pointer-events: none;
      }
    </style>
  </head>
  <body data-startup-error="${safeStartupError ? '1' : '0'}">
    <main>
      <h1>Authorize This Device</h1>
      <p data-testid="device-page-copy">Approve a waiting CLI login using your current web session.</p>
      <label for="user-code">Device code</label>
      <input id="user-code" data-testid="device-user-code" name="user_code" value="${safeUserCode}" placeholder="ABCD-1234" autocomplete="one-time-code" />
      <div id="session-state" data-testid="device-session-state">Checking your sign-in state…</div>
      <div class="row">
        <button id="authorize" data-testid="device-authorize" class="primary" type="button">Authorize device</button>
        <a id="sign-in" data-testid="device-sign-in" class="button secondary" href="#">Continue to sign in</a>
      </div>
      <div id="status" data-testid="device-status"></div>
    </main>
    <script>
      const startupError = ${JSON.stringify(safeStartupError)};
      const statusEl = document.getElementById('status');
      const sessionStateEl = document.getElementById('session-state');
      const authorizeButton = document.getElementById('authorize');
      const signInLink = document.getElementById('sign-in');
      const userCodeInput = document.getElementById('user-code');

      function setStatus(kind, message) {
        statusEl.className = kind || '';
        statusEl.textContent = message || '';
      }

      function syncSignInLink() {
        const signInURL = new URL('/auth/signin', window.location.origin);
        signInURL.searchParams.set('callbackUrl', window.location.href);
        signInLink.href = signInURL.toString();
      }

      async function refreshSession() {
        syncSignInLink();
        if (startupError) {
          sessionStateEl.textContent = startupError;
          setStatus('error', startupError);
          return null;
        }
        const response = await fetch('/auth/session', {
          credentials: 'include',
          headers: { Accept: 'application/json' },
        });
        if (!response.ok) {
          sessionStateEl.textContent = 'Sign in to approve this device.';
          signInLink.style.display = 'inline-flex';
          return null;
        }
        const session = await response.json();
        if (!session?.user?.username) {
          sessionStateEl.textContent = 'Sign in to approve this device.';
          signInLink.style.display = 'inline-flex';
          return null;
        }
        signInLink.style.display = 'none';
        sessionStateEl.textContent = 'Signed in as ' + session.user.username;
        return session;
      }

      authorizeButton.addEventListener('click', async () => {
        const userCode = (userCodeInput.value || '').trim().toUpperCase();
        if (!userCode) {
          setStatus('error', 'Enter the device code shown in the CLI.');
          return;
        }
        authorizeButton.disabled = true;
        setStatus('', 'Authorizing device…');
        try {
          const response = await fetch('/auth/device/approve', {
            method: 'POST',
            credentials: 'include',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ userCode }),
          });
          const payload = await response.json().catch(() => ({}));
          if (!response.ok) {
            throw new Error(payload?.error || payload?.message || 'Authorization failed');
          }
          setStatus('success', 'Device approved for ' + (payload?.user?.username || 'your account') + '.');
        } catch (error) {
          authorizeButton.disabled = false;
          setStatus('error', error?.message || 'Authorization failed');
        }
      });

      refreshSession().catch((error) => {
        setStatus('error', error?.message || 'Failed to load sign-in state');
        syncSignInLink();
        signInLink.style.display = 'inline-flex';
      });
    </script>
  </body>
</html>`;
}

export function renderDevicePageResponse(request) {
  const { startupError } = createAuthContext();
  const url = new URL(request.url);
  const userCode = url.searchParams.get('user_code') || '';
  return new Response(renderDevicePage({ userCode, startupError }), {
    status: 200,
    headers: {
      'Content-Type': 'text/html; charset=utf-8',
    },
  });
}
