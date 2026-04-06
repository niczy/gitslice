import { createAuthContext, getProxyAuthorizationHeader, loadSession } from '../../server/auth.js';
import { getConfiguredAPIBaseURL } from '../../shared/runtime.js';

function getGatewayTarget() {
  return getConfiguredAPIBaseURL(process.env, 'http://localhost:50051');
}

export async function action({ request }) {
  const { authSecret, startupError } = createAuthContext();
  if (startupError || !authSecret) {
    return Response.json({ error: startupError || 'Auth is not configured' }, { status: 500 });
  }

  let authorization = await getProxyAuthorizationHeader(request);
  if (!authorization) {
    const session = await loadSession(request);
    const username = String(session?.user?.username || '').trim();
    if (!username) {
      return Response.json({ error: 'Sign in required' }, { status: 401 });
    }
    authorization = `User ${username}`;
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

  let response;
  try {
    response = await fetch(new URL('/v1/auth/device/approve', getGatewayTarget()), {
      method: 'POST',
      headers: {
        Authorization: authorization,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ userCode }),
    });
  } catch {
    return Response.json({ error: 'Unable to reach the API origin for device approval' }, { status: 502 });
  }

  return new Response(await response.text(), {
    status: response.status,
    headers: {
      'Content-Type': response.headers.get('content-type') || 'application/json',
    },
  });
}
