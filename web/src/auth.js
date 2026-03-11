let cachedSession = null;

function normalizeSession(session) {
  const username = String(session?.user?.username || '').trim();
  if (!username) {
    return null;
  }
  return {
    ...session,
    user: {
      ...(session?.user || {}),
      username,
    },
    source: String(session?.source || '').trim() || 'oauth',
  };
}

export function getSignedInUsername() {
  return cachedSession?.user?.username || '';
}

export function getCachedSession() {
  return cachedSession;
}

export function setCachedSession(session) {
  cachedSession = normalizeSession(session);
  return cachedSession;
}

export async function signInWithAccount(_apiBaseUrl, username) {
  const trimmed = (username || '').trim();
  const response = await fetch('/auth/dev-login', {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify({ username: trimmed }),
  });

  if (!response.ok) {
    throw new Error('Invalid username');
  }

  const session = await response.json();
  const normalized = setCachedSession(session);
  return normalized?.user?.username || trimmed;
}

export async function fetchOAuthSession() {
  const response = await fetch('/auth/session', {
    credentials: 'include',
    headers: { Accept: 'application/json' },
  });
  if (!response.ok) {
    setCachedSession(null);
    return null;
  }
  return setCachedSession(await response.json());
}

export function startOAuthSignIn(providerId) {
  const callbackUrl = `${window.location.origin}/login`;
  const url = new URL(`/auth/signin/${providerId}`, window.location.origin);
  url.searchParams.set('callbackUrl', callbackUrl);
  window.location.assign(url.toString());
}

export function startOAuthSignOut() {
  const callbackUrl = `${window.location.origin}/`;
  const url = new URL('/auth/signout', window.location.origin);
  url.searchParams.set('callbackUrl', callbackUrl);
  window.location.assign(url.toString());
}

export async function signOutAccount() {
  try {
    await fetch('/auth/dev-logout', {
      method: 'POST',
      credentials: 'include',
      headers: { Accept: 'application/json' },
    });
  } catch {
    // best effort cookie clear
  }
  setCachedSession(null);
}
