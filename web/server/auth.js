import { createClerkClient } from '@clerk/backend';
import {
  base64URLToBytes,
  bytesToBase64URL,
  bytesToUTF8,
  decodeBase64URLUTF8,
  encodeBase64URLUTF8,
  getConfiguredAPIBaseURL,
  randomHex,
  signHMACSHA256Base64URL,
  timingSafeEqualText,
  utf8ToBytes,
} from '../shared/runtime.js';

const DEV_SESSION_COOKIE = 'gs_dev_session';
const LOCAL_SESSION_COOKIE = 'gs_local_session';
const LOCAL_SESSION_COOKIE_DEFAULT_MAX_AGE = 60 * 60 * 24 * 30;
const LOCAL_SESSION_REFRESH_SKEW_MS = 60 * 1000;
const CLERK_BRIDGE_CLAIMS_MAX_AGE_MS = 5 * 60 * 1000;
const USERNAME_PATTERN = /^[A-Za-z0-9][A-Za-z0-9_-]{2,31}$/;
const MAX_AUTH_ERROR_DETAIL_LENGTH = 240;
const localSessionKeyCache = new Map();

class ProviderIdentityError extends Error {
  constructor(message, status = 0) {
    super(message);
    this.name = 'ProviderIdentityError';
    this.status = status;
  }
}

function getGatewayTarget() {
  return getConfiguredAPIBaseURL(process.env, 'http://localhost:50051');
}

export function getAuthProvider() {
  return String(process.env.AUTH_PROVIDER || 'local').trim().toLowerCase() || 'local';
}

function normalizeAdminEmail(value) {
  const email = String(value || '').trim().replace(/^['"]|['"]$/g, '').toLowerCase();
  return email && email.includes('@') ? email : '';
}

function parseAdminEmailValue(rawValue) {
  const raw = String(rawValue || '').trim();
  if (!raw) {
    return [];
  }
  if (raw.startsWith('[')) {
    try {
      const parsed = JSON.parse(raw);
      if (Array.isArray(parsed)) {
        return parsed.map(normalizeAdminEmail).filter(Boolean);
      }
    } catch {
      // Fall back to delimiter parsing below.
    }
  }
  return raw.split(/[,;\n\t]+/).map(normalizeAdminEmail).filter(Boolean);
}

export function getConfiguredAdminEmails() {
  return new Set(parseAdminEmailValue(process.env.ADMIN_USER_EMAILS));
}

function attachAdminStatus(session) {
  if (!session?.user) {
    return session;
  }
  const admins = getConfiguredAdminEmails();
  const email = normalizeAdminEmail(session.user.email || session.user.primaryEmail);
  return {
    ...session,
    user: {
      ...session.user,
      isAdmin: Boolean(email && admins.has(email)),
      adminConfigured: admins.size > 0,
    },
  };
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
  if (provider !== 'clerk') {
    return true;
  }
  return isLocalHost(request?.url);
}

export function getPublicAuthConfig(request) {
  const authContext = createAuthContext(request);
  return {
    authProvider: String(authContext?.authProvider || getAuthProvider()).trim().toLowerCase() || 'local',
    allowDevLogin: isDevLoginEnabled(request, authContext),
    publicApiBaseUrl: getGatewayTarget(),
    adminConfigured: getConfiguredAdminEmails().size > 0,
  };
}

export function isClerkAuthConfigured() {
  const secretKey = String(process.env.CLERK_SECRET_KEY || '').trim();
  const publishableKey = String(process.env.CLERK_PUBLISHABLE_KEY || process.env.VITE_CLERK_PUBLISHABLE_KEY || '').trim();
  return Boolean(secretKey && publishableKey);
}

function requireCrypto() {
  if (!globalThis.crypto?.subtle || typeof globalThis.crypto?.getRandomValues !== 'function') {
    throw new Error('Web Crypto APIs are not available in the current runtime');
  }
  return globalThis.crypto;
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

function buildCookieSessionDisplayName(user) {
  const fullName = [user?.firstName, user?.lastName].filter(Boolean).join(' ').trim();
  return fullName || String(user?.email || user?.id || '').trim();
}

function buildGenericUserPayload({
  provider = 'clerk',
  id = '',
  email = '',
  name = '',
  username = '',
  profilePictureUrl = '',
} = {}) {
  const providerName = String(provider || '').trim().toLowerCase() || 'clerk';
  const normalizedName = String(name || '').trim();
  const normalizedEmail = String(email || '').trim();
  const normalizedID = String(id || '').trim();
  return {
    id: normalizedID,
    email: normalizedEmail,
    name: normalizedName,
    username: String(username || '').trim(),
    clerkUserId: providerName === 'clerk' ? normalizedID : '',
    derivedUsername: buildUsernameFromProfile({
      id: normalizedID,
      email: normalizedEmail,
      name: normalizedName,
    }),
    profilePictureUrl: String(profilePictureUrl || '').trim(),
  };
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

async function importLocalSessionKey(authSecret) {
  if (localSessionKeyCache.has(authSecret)) {
    return localSessionKeyCache.get(authSecret);
  }
  const crypto = requireCrypto();
  const digestPromise = crypto.subtle.digest('SHA-256', utf8ToBytes(`gs_local_session:${authSecret}`))
    .then((digest) => crypto.subtle.importKey('raw', digest, { name: 'AES-GCM' }, false, ['encrypt', 'decrypt']));
  localSessionKeyCache.set(authSecret, digestPromise);
  return digestPromise;
}

async function sealLocalSessionPayload(payload, authSecret) {
  const crypto = requireCrypto();
  const key = await importLocalSessionKey(authSecret);
  const iv = crypto.getRandomValues(new Uint8Array(12));
  const plaintext = utf8ToBytes(JSON.stringify(payload));
  const ciphertext = new Uint8Array(await crypto.subtle.encrypt({ name: 'AES-GCM', iv }, key, plaintext));
  return `${bytesToBase64URL(iv)}.${bytesToBase64URL(ciphertext)}`;
}

async function unsealLocalSessionPayload(rawValue, authSecret) {
  if (!rawValue || !authSecret) {
    return null;
  }
  const [encodedIV, encodedCiphertext] = String(rawValue).split('.');
  if (!encodedIV || !encodedCiphertext) {
    return null;
  }
  try {
    const crypto = requireCrypto();
    const key = await importLocalSessionKey(authSecret);
    const iv = base64URLToBytes(encodedIV);
    const ciphertext = base64URLToBytes(encodedCiphertext);
    const plaintext = await crypto.subtle.decrypt({ name: 'AES-GCM', iv }, key, ciphertext);
    return JSON.parse(bytesToUTF8(new Uint8Array(plaintext)));
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

function sanitizeAuthErrorCode(rawValue) {
  return String(rawValue || '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9._-]+/g, '_')
    .replace(/^_+|_+$/g, '')
    .slice(0, 64);
}

function sanitizeAuthErrorDetail(rawValue) {
  const value = String(rawValue || '')
    .replace(/[\r\n\t]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim();
  if (!value) {
    return '';
  }
  return value.slice(0, MAX_AUTH_ERROR_DETAIL_LENGTH);
}

function appendAuthFailure(failureURL, code, detail = '') {
  failureURL.searchParams.set('error', sanitizeAuthErrorCode(code) || 'auth_failed');
  const safeDetail = sanitizeAuthErrorDetail(detail);
  if (safeDetail) {
    failureURL.searchParams.set('detail', safeDetail);
  }
}

function primaryClerkEmailAddress(user) {
  const addresses = Array.isArray(user?.emailAddresses) ? user.emailAddresses : [];
  const primaryID = String(user?.primaryEmailAddressId || '').trim();
  if (primaryID) {
    const primary = addresses.find((entry) => String(entry?.id || '').trim() === primaryID);
    if (primary?.emailAddress) {
      return String(primary.emailAddress).trim();
    }
  }
  const fallback = addresses.find((entry) => entry?.emailAddress);
  return String(fallback?.emailAddress || '').trim();
}

function buildClerkSessionPayload(authObject, user) {
  const userID = String(authObject?.userId || user?.id || '').trim();
  if (!userID) {
    return null;
  }
  const email = primaryClerkEmailAddress(user);
  const name = String(user?.fullName || [user?.firstName, user?.lastName].filter(Boolean).join(' ') || email || userID).trim();
  return {
    user: buildGenericUserPayload({
      provider: 'clerk',
      id: userID,
      email,
      name,
      profilePictureUrl: user?.imageUrl || '',
    }),
    source: 'clerk',
    sessionId: String(authObject?.sessionId || '').trim(),
    organizationId: String(authObject?.orgId || '').trim(),
    authenticationMethod: 'clerk',
  };
}

function buildPendingClerkUsernameSession(session) {
  if (!session?.user?.clerkUserId && !session?.user?.id) {
    return null;
  }
  return {
    ...session,
    apiAuthSource: 'clerk_pending_username',
    requiresUsername: true,
    user: {
      ...(session.user || {}),
      username: '',
      localUsername: '',
      suggestedUsername: String(session?.user?.derivedUsername || '').trim(),
    },
  };
}

function parseTimeMs(rawValue) {
  const value = String(rawValue || '').trim();
  if (!value) {
    return 0;
  }
  const parsed = Date.parse(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

function sanitizeLocalSessionUser(user, fallbackUser = null) {
  const username = String(user?.username || fallbackUser?.username || fallbackUser?.localUsername || '').trim();
  if (!USERNAME_PATTERN.test(username)) {
    return null;
  }
  return {
    username,
    name: String(user?.name || fallbackUser?.name || '').trim(),
    email: String(user?.email || user?.primaryEmail || fallbackUser?.email || '').trim(),
    clerkUserId: String(user?.clerkUserId || fallbackUser?.clerkUserId || '').trim(),
    profilePictureUrl: String(user?.profilePictureUrl || fallbackUser?.profilePictureUrl || '').trim(),
  };
}

function normalizeLocalSessionPayload(payload) {
  const user = sanitizeLocalSessionUser(payload?.user);
  const refreshToken = String(payload?.refreshToken || '').trim();
  if (!user || !refreshToken) {
    return null;
  }
  const source = String(payload?.source || '').trim().toLowerCase() || 'local';
  return {
    source,
    sessionId: String(payload?.sessionId || '').trim(),
    accountId: String(payload?.accountId || '').trim(),
    linkedExistingUser: Boolean(payload?.linkedExistingUser),
    organizationId: String(payload?.organizationId || '').trim(),
    authenticationMethod: String(payload?.authenticationMethod || '').trim(),
    accessToken: String(payload?.accessToken || '').trim(),
    refreshToken,
    accessTokenExpiresAt: String(payload?.accessTokenExpiresAt || '').trim(),
    refreshTokenExpiresAt: String(payload?.refreshTokenExpiresAt || '').trim(),
    user,
  };
}

function shouldRefreshLocalSession(localSession) {
  if (!localSession?.accessToken) {
    return true;
  }
  const expiresAt = parseTimeMs(localSession.accessTokenExpiresAt);
  if (!expiresAt) {
    return false;
  }
  return expiresAt <= Date.now() + LOCAL_SESSION_REFRESH_SKEW_MS;
}

function isLocalRefreshExpired(localSession) {
  const expiresAt = parseTimeMs(localSession?.refreshTokenExpiresAt);
  if (!expiresAt) {
    return false;
  }
  return expiresAt <= Date.now() + LOCAL_SESSION_REFRESH_SKEW_MS;
}

function buildPublicSessionFromLocalSession(localSession) {
  const user = sanitizeLocalSessionUser(localSession?.user);
  if (!user) {
    return null;
  }
  return {
    user,
    source: String(localSession?.source || '').trim() || 'local',
    apiAuthSource: 'local_session',
    sessionId: String(localSession?.sessionId || '').trim(),
    accountId: String(localSession?.accountId || '').trim(),
    linkedExistingUser: Boolean(localSession?.linkedExistingUser),
    organizationId: String(localSession?.organizationId || '').trim(),
    authenticationMethod: String(localSession?.authenticationMethod || '').trim(),
    expires: String(localSession?.accessTokenExpiresAt || localSession?.refreshTokenExpiresAt || '').trim(),
  };
}

function buildLocalSessionPayloadFromAuth(authResponse, fallbackSession = null) {
  const user = sanitizeLocalSessionUser(authResponse?.user, fallbackSession?.user);
  const refreshToken = String(authResponse?.refreshToken || fallbackSession?.refreshToken || '').trim();
  if (!user || !refreshToken) {
    return null;
  }
  const source = String(authResponse?.source || fallbackSession?.source || '').trim().toLowerCase() || 'local';
  return normalizeLocalSessionPayload({
    source,
    sessionId: String(authResponse?.sessionId || fallbackSession?.sessionId || '').trim(),
    accountId: String(fallbackSession?.accountId || '').trim(),
    linkedExistingUser: Boolean(fallbackSession?.linkedExistingUser),
    organizationId: String(fallbackSession?.organizationId || '').trim(),
    authenticationMethod: String(fallbackSession?.authenticationMethod || '').trim(),
    accessToken: String(authResponse?.accessToken || '').trim(),
    refreshToken,
    accessTokenExpiresAt: String(authResponse?.accessTokenExpiresAt || '').trim(),
    refreshTokenExpiresAt: String(authResponse?.refreshTokenExpiresAt || fallbackSession?.refreshTokenExpiresAt || '').trim(),
    user,
  });
}

function buildLocalSessionPayloadFromEnsuredIdentity(ensuredSession) {
  return buildLocalSessionPayloadFromAuth(ensuredSession?.localAuth, ensuredSession);
}

function createClerkAuthContext(authSecret, request) {
  const secretKey = String(process.env.CLERK_SECRET_KEY || '').trim();
  const publishableKey = String(process.env.CLERK_PUBLISHABLE_KEY || process.env.VITE_CLERK_PUBLISHABLE_KEY || '').trim();
  if (!secretKey || !publishableKey) {
    return {
      authProvider: 'clerk',
      startupError: 'Clerk auth is not fully configured',
      authSecret,
    };
  }
  const authorizedParties = [];
  const requestOrigin = request ? new URL(request.url).origin : '';
  if (requestOrigin) {
    authorizedParties.push(requestOrigin);
  }
  const publicWebBaseURL = String(process.env.PUBLIC_WEB_BASE_URL || '').trim();
  if (publicWebBaseURL) {
    authorizedParties.push(publicWebBaseURL);
  }
  return {
    authProvider: 'clerk',
    startupError: '',
    authSecret,
    clerk: createClerkClient({
      secretKey,
      publishableKey,
    }),
    clerkPublishableKey: publishableKey,
    clerkJWTKey: String(process.env.CLERK_JWT_KEY || '').trim(),
    clerkAuthorizedParties: Array.from(new Set(authorizedParties.filter(Boolean))),
  };
}

export function createAuthContext(request) {
  const authSecret = process.env.AUTH_SECRET;
  if (!authSecret) {
    return { authProvider: getAuthProvider(), startupError: 'AUTH_SECRET is not configured' };
  }
  if (getAuthProvider() === 'clerk') {
    return createClerkAuthContext(authSecret, request);
  }
  return {
    authProvider: 'local',
    startupError: '',
    authSecret,
  };
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

async function serializeLocalSessionCookie(request, localSession, authSecret) {
  const sealed = await sealLocalSessionPayload(localSession, authSecret);
  const refreshExpiry = parseTimeMs(localSession?.refreshTokenExpiresAt);
  const maxAge = refreshExpiry
    ? Math.max(0, Math.floor((refreshExpiry - Date.now()) / 1000))
    : LOCAL_SESSION_COOKIE_DEFAULT_MAX_AGE;
  return serializeCookie(request, LOCAL_SESSION_COOKIE, sealed, maxAge);
}

export function clearLocalSessionCookie(request) {
  return serializeCookie(request, LOCAL_SESSION_COOKIE, '', 0);
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

async function authenticateClerkSession(request, authContext) {
  if (!authContext?.clerk) {
    return { session: null };
  }
  try {
    const requestState = await authContext.clerk.authenticateRequest(request, {
      authorizedParties: authContext.clerkAuthorizedParties,
      jwtKey: authContext.clerkJWTKey || undefined,
      acceptsToken: 'session_token',
    });
    if (!requestState?.isAuthenticated) {
      return { session: null };
    }
    const authObject = requestState.toAuth();
    const userID = String(authObject?.userId || '').trim();
    if (!userID) {
      return { session: null };
    }
    const user = await authContext.clerk.users.getUser(userID);
    return {
      session: buildClerkSessionPayload(authObject, user),
    };
  } catch {
    return { session: null };
  }
}

async function buildSignedClerkClaims(session, authSecret) {
  const now = Date.now();
  return signPayload({
    provider: 'clerk',
    userId: String(session?.user?.clerkUserId || session?.user?.id || '').trim(),
    sessionId: String(session?.sessionId || '').trim(),
    email: String(session?.user?.email || '').trim(),
    name: String(session?.user?.name || '').trim(),
    preferredUsername: String(session?.user?.derivedUsername || '').trim(),
    imageUrl: String(session?.user?.profilePictureUrl || '').trim(),
    issuedAtMs: now,
    expiresAtMs: now + CLERK_BRIDGE_CLAIMS_MAX_AGE_MS,
  }, authSecret);
}

async function ensureClerkLocalIdentity(request, authContext, session, options = {}) {
  const clerkUserID = String(session?.user?.clerkUserId || session?.user?.id || '').trim();
  if (!clerkUserID) {
    return session;
  }
  const claimToken = String(options?.claimToken || '').trim();
  const issueLocalSession = Boolean(options?.issueLocalSession);
  const preferredUsername = String(options?.preferredUsername || '').trim();
  const signedClaims = await buildSignedClerkClaims(session, authContext.authSecret);

  const response = await fetch(new URL('/v1/auth/clerk/ensure-local-identity', getGatewayTarget()), {
    method: 'POST',
    headers: {
      Accept: 'application/json',
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      signedClaims,
      claimToken,
      issueLocalSession,
      preferredUsername,
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
    throw new ProviderIdentityError(detail || `failed to ensure local Clerk identity (${response.status})`, response.status);
  }

  const payload = await response.json();
  const localUsername = String(payload?.user?.username || '').trim();
  return {
    ...session,
    accountId: String(payload?.accountId || '').trim(),
    linkedExistingUser: Boolean(payload?.linkedExistingUser),
    localAuth: payload?.localAuth || null,
    user: {
      ...(session.user || {}),
      username: localUsername,
      localUsername,
      name: String(payload?.user?.name || session.user?.name || '').trim(),
      email: String(payload?.user?.primaryEmail || session.user?.email || '').trim(),
    },
  };
}

function isClerkUsernameRequiredError(error) {
  return error instanceof ProviderIdentityError
    && /username required/i.test(String(error.message || ''));
}

async function loadLocalSession(request, authSecret) {
  const rawValue = parseCookieHeader(request.headers.get('cookie')).get(LOCAL_SESSION_COOKIE);
  if (!rawValue) {
    return { localSession: null, clearCookie: false };
  }
  const parsed = await unsealLocalSessionPayload(rawValue, authSecret);
  const localSession = normalizeLocalSessionPayload(parsed);
  if (!localSession || isLocalRefreshExpired(localSession)) {
    return { localSession: null, clearCookie: true };
  }
  return { localSession, clearCookie: false };
}

async function refreshLocalSession(request, authContext, localSession) {
  const response = await fetch(new URL('/v1/auth/token/refresh', getGatewayTarget()), {
    method: 'POST',
    headers: {
      Accept: 'application/json',
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      refreshToken: localSession.refreshToken,
    }),
  });
  if (!response.ok) {
    return { localSession: null, setCookie: '', clearCookie: true };
  }
  const authResponse = await response.json();
  const nextLocalSession = buildLocalSessionPayloadFromAuth(authResponse, localSession);
  if (!nextLocalSession) {
    return { localSession: null, setCookie: '', clearCookie: true };
  }
  return {
    localSession: nextLocalSession,
    setCookie: await serializeLocalSessionCookie(request, nextLocalSession, authContext.authSecret),
    clearCookie: false,
  };
}

async function bootstrapLocalSessionFromClerk(request, authContext, session, claimToken = '') {
  if (!session) {
    return { localSession: null, setCookie: '' };
  }
  const ensuredSession = await ensureClerkLocalIdentity(request, authContext, session, {
    claimToken,
    issueLocalSession: true,
  });
  const localSession = buildLocalSessionPayloadFromEnsuredIdentity(ensuredSession);
  if (!localSession) {
    throw new Error('failed to create local session from Clerk identity');
  }
  return {
    localSession,
    setCookie: await serializeLocalSessionCookie(request, localSession, authContext.authSecret),
  };
}

async function resolveClerkBackedLocalSession(request, authContext, options = {}) {
  const setCookies = [];
  let { localSession, clearCookie: clearLocalCookie } = await loadLocalSession(request, authContext.authSecret);
  if (localSession && String(localSession.source || '').trim() !== 'clerk') {
    localSession = null;
    clearLocalCookie = true;
  }
  if (localSession && !shouldRefreshLocalSession(localSession)) {
    return {
      localSession,
      publicSession: buildPublicSessionFromLocalSession(localSession),
      setCookies,
      clearLocalCookie,
    };
  }
  if (localSession) {
    const refreshedSession = await refreshLocalSession(request, authContext, localSession);
    if (refreshedSession.setCookie) {
      setCookies.push(refreshedSession.setCookie);
    }
    if (refreshedSession.localSession) {
      return {
        localSession: refreshedSession.localSession,
        publicSession: buildPublicSessionFromLocalSession(refreshedSession.localSession),
        setCookies,
        clearLocalCookie: false,
      };
    }
    clearLocalCookie = clearLocalCookie || refreshedSession.clearCookie;
  }

  const clerkAuthSession = await authenticateClerkSession(request, authContext);
  if (!clerkAuthSession.session) {
    return {
      localSession: null,
      publicSession: null,
      setCookies,
      clearLocalCookie: clearLocalCookie || Boolean(localSession),
    };
  }

  let bootstrapped;
  try {
    bootstrapped = await bootstrapLocalSessionFromClerk(request, authContext, clerkAuthSession.session, options.claimToken || '');
  } catch (error) {
    if (isClerkUsernameRequiredError(error)) {
      return {
        localSession: null,
        publicSession: buildPendingClerkUsernameSession(clerkAuthSession.session),
        setCookies,
        clearLocalCookie: clearLocalCookie || Boolean(localSession),
      };
    }
    throw error;
  }
  if (bootstrapped.setCookie) {
    setCookies.push(bootstrapped.setCookie);
  }
  return {
    localSession: bootstrapped.localSession,
    publicSession: buildPublicSessionFromLocalSession(bootstrapped.localSession),
    setCookies,
    clearLocalCookie: false,
  };
}

export async function getProxyAuthorizationHeader(request) {
  const { authorization } = await getProxyAuthorizationResult(request);
  return authorization;
}

export async function getClerkAdminClaimsResult(request) {
  const authContext = createAuthContext(request);
  if (authContext.authProvider !== 'clerk' || authContext.startupError) {
    return { signedClaims: '', rejectUnauthenticated: false };
  }
  const clerkAuthSession = await authenticateClerkSession(request, authContext);
  if (!clerkAuthSession.session) {
    return { signedClaims: '', rejectUnauthenticated: true };
  }
  return {
    signedClaims: await buildSignedClerkClaims(clerkAuthSession.session, authContext.authSecret),
    rejectUnauthenticated: false,
  };
}

export async function getProxyAuthorizationResult(request) {
  const authContext = createAuthContext(request);
  if (authContext.authProvider !== 'clerk' || authContext.startupError) {
    return { authorization: '', setCookies: [], rejectUnauthenticated: false };
  }
  const cookieHeader = String(request.headers.get('cookie') || '');
  const hadSessionCookie = cookieHeader.includes(`${LOCAL_SESSION_COOKIE}=`);
  const authSession = await resolveClerkBackedLocalSession(request, authContext);
  const setCookies = [...(authSession.setCookies || [])];
  if (authSession.clearLocalCookie) {
    setCookies.push(clearLocalSessionCookie(request));
  }
  return {
    authorization: authSession.localSession?.accessToken ? `Bearer ${authSession.localSession.accessToken}` : '',
    setCookies,
    rejectUnauthenticated: hadSessionCookie && !authSession.localSession,
  };
}

export async function loadSession(request) {
  const authContext = createAuthContext(request);
  if (!authContext?.authSecret) {
    return null;
  }
  const allowDevLogin = isDevLoginEnabled(request, authContext);
  if (authContext.authProvider === 'clerk') {
    const resolvedSession = await resolveClerkBackedLocalSession(request, authContext);
    if (!resolvedSession.publicSession) {
      if (!allowDevLogin) {
        return null;
      }
      const devUsername = await verifyDevSession(parseCookieHeader(request.headers.get('cookie')).get(DEV_SESSION_COOKIE), authContext.authSecret);
      if (!devUsername) {
        return null;
      }
      return attachAdminStatus({
        user: {
          name: devUsername,
          username: devUsername,
        },
        source: 'dev',
        expires: '',
      });
    }
    return attachAdminStatus(resolvedSession.publicSession);
  }
  const devUsername = await verifyDevSession(parseCookieHeader(request.headers.get('cookie')).get(DEV_SESSION_COOKIE), authContext.authSecret);
  if (!devUsername) {
    return null;
  }

  return attachAdminStatus({
    user: {
      name: devUsername,
      username: devUsername,
    },
    source: 'dev',
    expires: '',
  });
}

export async function handleSessionRequest(request) {
  const authContext = createAuthContext(request);
  const allowDevLogin = isDevLoginEnabled(request, authContext);
  if (authContext.startupError && !(allowDevLogin && authContext.authSecret)) {
    return Response.json({ error: authContext.startupError }, { status: 500 });
  }

  if (authContext.authProvider === 'clerk') {
    let session = null;
    const setCookies = [];
    if (!authContext.startupError) {
      try {
        const resolvedSession = await resolveClerkBackedLocalSession(request, authContext);
        session = resolvedSession.publicSession;
        setCookies.push(...(resolvedSession.setCookies || []));
        if (resolvedSession.clearLocalCookie) {
          setCookies.push(clearLocalSessionCookie(request));
        }
      } catch (error) {
        return Response.json({ error: error instanceof Error ? error.message : 'Failed to load provider-backed session' }, { status: 500 });
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

    const response = session ? Response.json(attachAdminStatus(session)) : Response.json({ error: 'Not signed in' }, { status: 401 });
    for (const cookie of setCookies) {
      if (cookie) {
        response.headers.append('Set-Cookie', cookie);
      }
    }
    return response;
  }

  const session = await loadSession(request);
  if (!session?.user?.username) {
    return Response.json({ error: 'Not signed in' }, { status: 401 });
  }
  return Response.json(attachAdminStatus(session));
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
  const response = new Response(JSON.stringify({ ok: true }), {
    status: 200,
    headers: {
      'Content-Type': 'application/json',
    },
  });
  response.headers.append('Set-Cookie', serializeCookie(request, DEV_SESSION_COOKIE, '', 0));
  response.headers.append('Set-Cookie', clearLocalSessionCookie(request));
  return response;
}

async function handleClerkClaimAccountRequest(request, authContext) {
  const url = new URL(request.url);
  const claimToken = String(url.searchParams.get('token') || '').trim();
  const callbackURL = normalizeReturnTo(request, url.searchParams.get('callbackUrl'), '/browser');
  if (!claimToken) {
    return Response.json({ error: 'claim token is required' }, { status: 400 });
  }

  const { session } = await authenticateClerkSession(request, authContext);
  if (!session) {
    const signinURL = new URL('/sign-in', request.url);
    signinURL.searchParams.set('redirect_url', url.toString());
    return redirectWithCookies(signinURL.toString(), []);
  }

  try {
    const ensuredSession = await ensureClerkLocalIdentity(request, authContext, session, {
      claimToken,
      issueLocalSession: true,
    });
    const localSession = buildLocalSessionPayloadFromEnsuredIdentity(ensuredSession);
    const cookies = [];
    if (localSession) {
      cookies.push(await serializeLocalSessionCookie(request, localSession, authContext.authSecret));
    }
    return redirectWithCookies(callbackURL, cookies);
  } catch (error) {
    return Response.json({ error: error instanceof Error ? error.message : 'Failed to claim account' }, { status: 400 });
  }
}

async function handleClerkCompleteUsernameRequest(request, authContext) {
  if (request.method !== 'POST') {
    return Response.json({ error: 'Method not allowed' }, { status: 405 });
  }

  let payload = {};
  try {
    payload = await request.json();
  } catch {
    return Response.json({ error: 'Invalid JSON body' }, { status: 400 });
  }

  const preferredUsername = String(payload?.username || payload?.preferredUsername || '').trim().toLowerCase();
  if (!USERNAME_PATTERN.test(preferredUsername)) {
    return Response.json({ error: 'Invalid username' }, { status: 400 });
  }

  const { session } = await authenticateClerkSession(request, authContext);
  if (!session) {
    return Response.json({ error: 'Not signed in' }, { status: 401 });
  }

  try {
    const ensuredSession = await ensureClerkLocalIdentity(request, authContext, session, {
      preferredUsername,
      issueLocalSession: true,
    });
    const localSession = buildLocalSessionPayloadFromEnsuredIdentity(ensuredSession);
    const publicSession = localSession ? buildPublicSessionFromLocalSession(localSession) : null;
    if (!localSession || !publicSession) {
      return Response.json({ error: 'Failed to create local session' }, { status: 500 });
    }
    const response = Response.json(publicSession);
    response.headers.append('Set-Cookie', await serializeLocalSessionCookie(request, localSession, authContext.authSecret));
    return response;
  } catch (error) {
    const message = error instanceof Error ? error.message : 'Failed to choose username';
    const statusCode = /already (taken|exists)/i.test(message)
      ? 409
      : error instanceof ProviderIdentityError && error.status >= 400 && error.status < 500
        ? error.status
        : 400;
    return Response.json({ error: message }, { status: statusCode });
  }
}

export async function handleAuthRequest(request) {
  const authContext = createAuthContext(request);
  if (authContext.startupError) {
    return Response.json({ error: authContext.startupError || 'Auth is not configured' }, { status: 500 });
  }
  if (authContext.authProvider === 'clerk') {
    const pathname = new URL(request.url).pathname.replace(/^\/auth\/?/, '');
    if (pathname === 'claim-account') {
      return handleClerkClaimAccountRequest(request, authContext);
    }
    if (pathname === 'clerk/complete-username') {
      return handleClerkCompleteUsernameRequest(request, authContext);
    }
    return Response.json({ error: 'Clerk auth route not found' }, { status: 404 });
  }
  return Response.json({ error: 'Auth route not found' }, { status: 404 });
}

export const __test = {
  async createLocalSessionCookieValue(payload, authSecret) {
    const normalized = normalizeLocalSessionPayload(payload);
    if (!normalized) {
      throw new Error('invalid local session payload');
    }
    return sealLocalSessionPayload(normalized, authSecret);
  },
};

export function renderDevicePage({ userCode, startupError, authProvider }) {
  const safeUserCode = escapeHTML(userCode);
  const safeStartupError = escapeHTML(startupError);
  const safeAuthProvider = escapeHTML(authProvider);
  return `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>Authorize Device</title>
    <link rel="preconnect" href="https://fonts.googleapis.com">
    <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
    <link href="https://fonts.googleapis.com/css2?family=IBM+Plex+Sans:wght@400;500;600;700&display=swap" rel="stylesheet">
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
        font-family: "IBM Plex Sans", ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
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
  <body data-startup-error="${safeStartupError ? '1' : '0'}" data-auth-provider="${safeAuthProvider}">
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
        const authProvider = document.body.dataset.authProvider || 'local';
        const signInURL = new URL(authProvider === 'clerk' ? '/sign-in' : '/login', window.location.origin);
        if (authProvider === 'clerk') {
          signInURL.searchParams.set('redirect_url', window.location.href);
        } else {
          signInURL.searchParams.set('redirect_url', window.location.href);
        }
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
  const { startupError, authProvider } = createAuthContext(request);
  const url = new URL(request.url);
  const userCode = url.searchParams.get('user_code') || '';
  return new Response(renderDevicePage({ userCode, startupError, authProvider }), {
    status: 200,
    headers: {
      'Content-Type': 'text/html; charset=utf-8',
    },
  });
}
