import { getActiveClerkSessionToken, getSignedInAuthSource, getSignedInUsername } from '../auth.js';

// Browser data requests stay same-origin so auth cookies continue to work
// when the web tier proxies API traffic to a different origin.
export const apiBaseUrl = '';

export function currentUsername() {
  return getSignedInUsername();
}

export async function fetchWithAuth(url, options = {}) {
  const headers = new Headers(options.headers || {});
  const authSource = getSignedInAuthSource();
  const username = currentUsername();
  if (username && authSource !== 'clerk') {
    headers.set('Authorization', `User ${username}`);
  } else if (!headers.has('Authorization')) {
    const clerkToken = await getActiveClerkSessionToken();
    if (clerkToken) {
      headers.set('Authorization', `Bearer ${clerkToken}`);
    }
  }
  return fetch(url, { ...options, credentials: 'include', headers });
}

export async function readErrorMessage(response, fallback) {
  let detail = '';
  try {
    const text = await response.text();
    if (text) {
      try {
        const payload = JSON.parse(text);
        detail = payload?.message || payload?.error || '';
      } catch {
        detail = text;
      }
    }
  } catch {
    detail = '';
  }
  return detail ? `${fallback}: ${detail}` : `${fallback} (${response.status})`;
}

export function encodeBase64(bytes) {
  let binary = '';
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary);
}
