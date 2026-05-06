let cachedSession = null;

function normalizeSession(session) {
  const source = String(session?.source || '').trim() || 'workos';
  const username = String(session?.user?.username || '').trim();
  const workosUserId = String(session?.user?.workosUserId || session?.user?.id || '').trim();
  const clerkUserId = String(session?.user?.clerkUserId || session?.user?.id || '').trim();
  if (!username && !(source === 'workos' && workosUserId) && !(source === 'clerk' && clerkUserId)) {
    return null;
  }
  return {
    ...session,
    user: {
      ...(session?.user || {}),
      username,
      workosUserId,
      clerkUserId,
    },
    source,
  };
}

export function getSignedInUsername() {
  return cachedSession?.user?.username || '';
}

export function getSignedInAuthSource() {
  return String(cachedSession?.source || '').trim();
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

export function startOAuthSignIn(provider = 'workos') {
  const normalizedProvider = String(provider || 'workos').trim().toLowerCase();
  const callbackUrl = `${window.location.origin}/slices`;
  const url = new URL(normalizedProvider === 'clerk' ? '/sign-in' : '/auth/signin/workos', window.location.origin);
  if (normalizedProvider === 'clerk') {
    url.searchParams.set('redirect_url', callbackUrl);
  } else {
    url.searchParams.set('callbackUrl', callbackUrl);
  }
  window.location.assign(url.toString());
}

export function startOAuthSignOut(provider = 'workos') {
  const normalizedProvider = String(provider || 'workos').trim().toLowerCase();
  const callbackUrl = `${window.location.origin}/`;
  const url = new URL(normalizedProvider === 'clerk' ? '/sign-out' : '/auth/signout', window.location.origin);
  if (normalizedProvider === 'clerk') {
    url.searchParams.set('redirect_url', callbackUrl);
  } else {
    url.searchParams.set('callbackUrl', callbackUrl);
  }
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
