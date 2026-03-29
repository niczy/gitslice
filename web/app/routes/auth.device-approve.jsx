import { createAuthContext, loadSession } from '../../server/auth.js';
import { getConfiguredAPIBaseURL } from '../../shared/runtime.js';

const gatewayTarget = getConfiguredAPIBaseURL(process.env, 'http://localhost:50051');

export async function action({ request }) {
  const { authSecret, startupError } = createAuthContext();
  if (startupError || !authSecret) {
    return Response.json({ error: startupError || 'Auth is not configured' }, { status: 500 });
  }

  const session = await loadSession(request);
  const username = String(session?.user?.username || '').trim();
  if (!username) {
    return Response.json({ error: 'Sign in required' }, { status: 401 });
  }

  let payload;
  try {
    payload = await request.json();
  } catch {
    return Response.json({ error: 'Invalid JSON body' }, { status: 400 });
  }

  const userCode = String(payload?.userCode || '').trim().toUpperCase();
  if (!userCode) {
    return Response.json({ error: 'userCode is required' }, { status: 400 });
  }

  const response = await fetch(new URL('/v1/auth/device/approve', gatewayTarget), {
    method: 'POST',
    headers: {
      Authorization: `User ${username}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ userCode }),
  });

  return new Response(await response.text(), {
    status: response.status,
    headers: {
      'Content-Type': response.headers.get('content-type') || 'application/json',
    },
  });
}
