import { createHmac, randomBytes, timingSafeEqual } from 'node:crypto';

import { Auth } from '@auth/core';
import GitHub from '@auth/core/providers/github';
import Google from '@auth/core/providers/google';

const DEV_SESSION_COOKIE = 'gs_dev_session';
const USERNAME_PATTERN = /^[A-Za-z0-9][A-Za-z0-9_-]{2,31}$/;
const gatewayTarget = process.env.VITE_FILE_API_PROXY_TARGET || 'http://localhost:8080';

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
  return `${trimmed}${randomBytes(2).toString('hex')}`.slice(0, 8);
}

export function createAuthContext() {
  const authSecret = process.env.AUTH_SECRET;
  if (!authSecret) {
    return { startupError: 'AUTH_SECRET is not configured' };
  }

  const providers = [];
  if (process.env.AUTH_GOOGLE_ID && process.env.AUTH_GOOGLE_SECRET) {
    providers.push(
      Google({
        clientId: process.env.AUTH_GOOGLE_ID,
        clientSecret: process.env.AUTH_GOOGLE_SECRET,
      }),
    );
  }
  if (process.env.AUTH_GITHUB_ID && process.env.AUTH_GITHUB_SECRET) {
    providers.push(
      GitHub({
        clientId: process.env.AUTH_GITHUB_ID,
        clientSecret: process.env.AUTH_GITHUB_SECRET,
      }),
    );
  }

  return {
    startupError: '',
    authSecret,
    authConfig: {
      trustHost: true,
      secret: authSecret,
      basePath: '/auth',
      session: { strategy: 'jwt' },
      providers,
      callbacks: {
        async jwt({ token, profile }) {
          if (profile) {
            token.username = buildUsernameFromProfile(profile);
          }
          return token;
        },
        async session({ session, token }) {
          if (!session.user) {
            session.user = {};
          }
          session.user.username = token.username;
          return session;
        },
      },
    },
  };
}

function encodeBase64URL(value) {
  return Buffer.from(value, 'utf8').toString('base64url');
}

function decodeBase64URL(value) {
  return Buffer.from(value, 'base64url').toString('utf8');
}

function signDevSession(username, authSecret) {
  const payload = encodeBase64URL(JSON.stringify({ username }));
  const signature = createHmac('sha256', authSecret).update(payload).digest('base64url');
  return `${payload}.${signature}`;
}

function verifyDevSession(rawValue, authSecret) {
  if (!rawValue || !authSecret) {
    return '';
  }
  const [payload, signature] = String(rawValue).split('.');
  if (!payload || !signature) {
    return '';
  }
  const expected = createHmac('sha256', authSecret).update(payload).digest('base64url');
  const actualBuffer = Buffer.from(signature, 'utf8');
  const expectedBuffer = Buffer.from(expected, 'utf8');
  if (actualBuffer.length !== expectedBuffer.length || !timingSafeEqual(actualBuffer, expectedBuffer)) {
    return '';
  }
  try {
    const parsed = JSON.parse(decodeBase64URL(payload));
    const username = String(parsed?.username || '').trim();
    return USERNAME_PATTERN.test(username) ? username : '';
  } catch {
    return '';
  }
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

export async function loadAuthJsSession(request, authConfig) {
  if (!authConfig) {
    return null;
  }
  const sessionURL = new URL('/auth/session', request.url);
  const response = await Auth(
    new Request(sessionURL, {
      method: 'GET',
      headers: request.headers,
    }),
    authConfig,
  );
  if (!response.ok) {
    return null;
  }
  return response.json();
}

export async function loadSession(request) {
  const { authConfig, authSecret } = createAuthContext();
  if (!authSecret) {
    return null;
  }

  const authSession = await loadAuthJsSession(request, authConfig);
  const oauthUsername = String(authSession?.user?.username || '').trim();
  if (oauthUsername) {
    return {
      ...authSession,
      source: 'oauth',
      user: {
        ...(authSession?.user || {}),
        username: oauthUsername,
      },
    };
  }

  const devUsername = verifyDevSession(parseCookieHeader(request.headers.get('cookie')).get(DEV_SESSION_COOKIE), authSecret);
  if (!devUsername) {
    return null;
  }

  return {
    user: {
      name: devUsername,
      username: devUsername,
    },
    source: 'dev',
    expires: '',
  };
}

export async function handleSessionRequest(request) {
  const { startupError } = createAuthContext();
  if (startupError) {
    return Response.json({ error: startupError }, { status: 500 });
  }
  const session = await loadSession(request);
  if (!session?.user?.username) {
    return Response.json({ error: 'Not signed in' }, { status: 401 });
  }
  return Response.json(session);
}

export async function handleDevLoginRequest(request) {
  const { authSecret, startupError } = createAuthContext();
  if (startupError || !authSecret) {
    return Response.json({ error: startupError || 'Auth is not configured' }, { status: 500 });
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

  const loginResponse = await fetch(new URL('/v1/auth/login', gatewayTarget), {
    method: 'POST',
    headers: {
      Accept: 'application/json',
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ username }),
  });
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
      'Set-Cookie': serializeCookie(request, DEV_SESSION_COOKIE, signDevSession(username, authSecret), 60 * 60 * 24 * 30),
    },
  });
}

export function handleDevLogoutRequest(request) {
  return new Response(JSON.stringify({ ok: true }), {
    status: 200,
    headers: {
      'Content-Type': 'application/json',
      'Set-Cookie': serializeCookie(request, DEV_SESSION_COOKIE, '', 0),
    },
  });
}

export async function handleAuthRequest(request) {
  const { authConfig, startupError } = createAuthContext();
  if (startupError || !authConfig) {
    return Response.json({ error: startupError || 'Auth is not configured' }, { status: 500 });
  }
  return Auth(request, authConfig);
}

export function renderDevicePage({ userCode, startupError }) {
  const safeUserCode = escapeHTML(userCode);
  const safeStartupError = escapeHTML(startupError);
  return `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>Authorize Device</title>
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
        font-family: "Iowan Old Style", "Palatino Linotype", "Book Antiqua", serif;
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
  <body data-startup-error="${safeStartupError ? '1' : '0'}">
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
        const signInURL = new URL('/auth/signin', window.location.origin);
        signInURL.searchParams.set('callbackUrl', window.location.href);
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
  const { startupError } = createAuthContext();
  const url = new URL(request.url);
  const userCode = url.searchParams.get('user_code') || '';
  return new Response(renderDevicePage({ userCode, startupError }), {
    status: 200,
    headers: {
      'Content-Type': 'text/html; charset=utf-8',
    },
  });
}
