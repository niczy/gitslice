let cachedSession = null;

function getCurrentOAuthReturnUrl() {
  if (typeof window === 'undefined') {
    return '/slices';
  }
  const url = new URL(window.location.href);
  if (
    url.pathname === '/login'
    || url.pathname === '/sign-in'
    || url.pathname.startsWith('/sign-in/')
    || url.pathname === '/sign-up'
    || url.pathname.startsWith('/sign-up/')
    || url.pathname === '/sign-out'
    || url.pathname.startsWith('/auth/')
  ) {
    return `${window.location.origin}/slices`;
  }
  return url.toString();
}

async function getActiveClerkSessionToken() {
  if (typeof window === 'undefined') {
    return '';
  }
  try {
    let clerk = window.Clerk;
    if (!(clerk?.status === 'ready' || clerk?.status === 'degraded' || (clerk?.loaded && !clerk?.status))) {
      const readyPromise = window.__clerk_internal_ready;
      if (readyPromise && typeof readyPromise.then === 'function') {
        await Promise.race([
          readyPromise,
          new Promise((resolve) => setTimeout(resolve, 2000)),
        ]);
        clerk = window.Clerk;
      }
    }
    const token = await clerk?.session?.getToken?.({ skipCache: true });
    return typeof token === 'string' ? token.trim() : '';
  } catch {
    return '';
  }
}

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

export async function completeClerkUsername(username) {
  const headers = new Headers({ 'Content-Type': 'application/json', Accept: 'application/json' });
  const clerkToken = await getActiveClerkSessionToken();
  if (clerkToken) {
    headers.set('Authorization', `Bearer ${clerkToken}`);
  }
  const response = await fetch('/auth/clerk/complete-username', {
    method: 'POST',
    credentials: 'include',
    headers,
    body: JSON.stringify({ username: String(username || '').trim().toLowerCase() }),
  });
  if (!response.ok) {
    let detail = '';
    try {
      const payload = await response.json();
      detail = String(payload?.error || payload?.message || '').trim();
    } catch {
      detail = '';
    }
    throw new Error(detail || 'Unable to choose username.');
  }
  return setCachedSession(await response.json());
}

export function startOAuthSignIn(provider = 'workos', callbackUrl = '') {
  const normalizedProvider = String(provider || 'workos').trim().toLowerCase();
  const returnUrl = String(callbackUrl || '').trim() || getCurrentOAuthReturnUrl();
  const url = new URL(normalizedProvider === 'clerk' ? '/sign-in' : '/auth/signin/workos', window.location.origin);
  if (normalizedProvider === 'clerk') {
    url.searchParams.set('redirect_url', returnUrl);
  } else {
    url.searchParams.set('callbackUrl', returnUrl);
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
