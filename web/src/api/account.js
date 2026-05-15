import { apiBaseUrl, fetchWithAuth, readErrorMessage } from './client.js';

export async function fetchCurrentUser() {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/users/me`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load profile'));
  }
  return response.json();
}

export async function updateCurrentUser({ name = '', primaryEmail = '' } = {}) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/users/me`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      name,
      primaryEmail,
    }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to update profile'));
  }
  return response.json();
}

export async function deleteCurrentUser() {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/users/me`, {
    method: 'DELETE',
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to delete account'));
  }
}

export async function fetchAuthSessions() {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/auth/sessions`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load sessions'));
  }
  const payload = await response.json();
  return payload?.sessions || [];
}

export async function fetchAuthContext() {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/auth/context`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load auth context'));
  }
  return response.json();
}

export async function fetchAdminStatus() {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/admin/status`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load admin status'));
  }
  return response.json();
}

export async function deleteAdminUserByEmail(email) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/admin/users:deleteByEmail`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email }),
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to delete user'));
  }
  return response.json();
}

export async function deleteAuthSession(sessionId) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/auth/sessions/${encodeURIComponent(sessionId)}`, {
    method: 'DELETE',
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to revoke session'));
  }
}

export async function fetchAuthMethods() {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/auth/methods`);
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to load auth methods'));
  }
  const payload = await response.json();
  return payload?.methods || [];
}

export async function deleteAuthMethod(methodId) {
  const response = await fetchWithAuth(`${apiBaseUrl}/v1/auth/methods/${encodeURIComponent(methodId)}`, {
    method: 'DELETE',
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Unable to remove auth method'));
  }
}
