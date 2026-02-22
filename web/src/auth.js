const STORAGE_KEY = 'gs_auth_username';

function readStoredUsername() {
  try {
    return window.localStorage.getItem(STORAGE_KEY) || '';
  } catch {
    return '';
  }
}

function writeStoredUsername(username) {
  try {
    if (username) {
      window.localStorage.setItem(STORAGE_KEY, username);
    } else {
      window.localStorage.removeItem(STORAGE_KEY);
    }
  } catch {
    // ignore storage errors (private mode, blocked storage, etc.)
  }
}

export function getSignedInUsername() {
  return readStoredUsername();
}

export async function signInWithAccount(apiBaseUrl, username) {
  const trimmed = (username || '').trim();
  const response = await fetch(`${apiBaseUrl}/v1/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username: trimmed }),
  });

  if (!response.ok) {
    throw new Error('Invalid username');
  }

  writeStoredUsername(trimmed);
  return trimmed;
}

export async function fetchOAuthSession() {
  const response = await fetch('/auth/session', {
    credentials: 'include',
    headers: { Accept: 'application/json' },
  });
  if (!response.ok) {
    return null;
  }
  return response.json();
}

export function startOAuthSignIn(providerId, redirectTo = '') {
  const callbackPath = redirectTo ? `#/login?redirect=${encodeURIComponent(redirectTo)}` : '#/login';
  const callbackUrl = `${window.location.origin}/${callbackPath}`;
  const url = new URL(`/auth/signin/${providerId}`, window.location.origin);
  url.searchParams.set('callbackUrl', callbackUrl);
  window.location.assign(url.toString());
}

export function startOAuthSignOut() {
  const callbackUrl = `${window.location.origin}/#/landing`;
  const url = new URL('/auth/signout', window.location.origin);
  url.searchParams.set('callbackUrl', callbackUrl);
  window.location.assign(url.toString());
}

export function signOutAccount() {
  writeStoredUsername('');
}
