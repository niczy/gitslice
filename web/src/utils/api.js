// ---------------------------------------------------------------------------
// API and authentication helpers
// ---------------------------------------------------------------------------

export const apiBaseUrl = import.meta.env.VITE_FILE_API_BASE_URL || '';

export function currentUsername() {
  try {
    return window.localStorage.getItem('gs_username') || '';
  } catch {
    return '';
  }
}

export function fetchWithAuth(url, options = {}) {
  const headers = new Headers(options.headers || {});
  const username = currentUsername();
  if (username) {
    headers.set('Authorization', `User ${username}`);
  }
  return fetch(url, { ...options, headers });
}
