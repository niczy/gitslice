import { createHmac, randomBytes, timingSafeEqual } from 'node:crypto';
import { Auth } from '@auth/core';
import Google from '@auth/core/providers/google';
import GitHub from '@auth/core/providers/github';

const DEV_SESSION_COOKIE = 'gs_dev_session';
const USERNAME_PATTERN = /^[A-Za-z0-9][A-Za-z0-9_-]{2,31}$/;

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

function createAuthContext() {
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
    authSecret,
  };
}

function requestOrigin(req) {
  const host = req.headers.host || 'localhost';
  const forwardedProto = String(req.headers['x-forwarded-proto'] || '').trim();
  const proto = forwardedProto || (host.startsWith('localhost') || host.startsWith('127.0.0.1') ? 'http' : 'https');
  return `${proto}://${host}`;
}

function toAuthRequest(req, targetURL, methodOverride) {
  const method = methodOverride || req.method || 'GET';
  const body = ['GET', 'HEAD'].includes(method) ? undefined : req;
  return new Request(targetURL, {
    method,
    headers: req.headers,
    body,
    duplex: body ? 'half' : undefined,
  });
}

function authRouteURL(req) {
  const path = String(req.url || '/');
  if (path.startsWith('/auth')) {
    return new URL(path, requestOrigin(req)).toString();
  }
  const normalized = path.startsWith('/') ? path : `/${path}`;
  return new URL(`/auth${normalized}`, requestOrigin(req)).toString();
}

async function sendResponse(res, response) {
  res.statusCode = response.status;
  response.headers.forEach((value, key) => {
    res.setHeader(key, value);
  });
  if (String(res.req?.method || '').toUpperCase() === 'HEAD') {
    res.end();
    return;
  }
  const payload = await response.arrayBuffer();
  res.end(Buffer.from(payload));
}

function json(res, statusCode, payload) {
  res.statusCode = statusCode;
  res.setHeader('Content-Type', 'application/json');
  if (String(res.req?.method || '').toUpperCase() === 'HEAD') {
    res.end();
    return;
  }
  res.end(JSON.stringify(payload));
}

function parseCookies(req) {
  const raw = String(req.headers.cookie || '');
  const cookies = new Map();
  for (const chunk of raw.split(';')) {
    const trimmed = chunk.trim();
    if (!trimmed) {
      continue;
    }
    const separator = trimmed.indexOf('=');
    if (separator < 0) {
      continue;
    }
    const name = trimmed.slice(0, separator).trim();
    const value = trimmed.slice(separator + 1).trim();
    cookies.set(name, value);
  }
  return cookies;
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

function appendSetCookie(res, value) {
  const existing = res.getHeader('Set-Cookie');
  if (!existing) {
    res.setHeader('Set-Cookie', value);
    return;
  }
  const next = Array.isArray(existing) ? [...existing, value] : [existing, value];
  res.setHeader('Set-Cookie', next);
}

function serializeCookie(req, name, value, maxAgeSeconds) {
  const parts = [
    `${name}=${value}`,
    'Path=/',
    'HttpOnly',
    'SameSite=Lax',
  ];
  if (typeof maxAgeSeconds === 'number') {
    parts.push(`Max-Age=${maxAgeSeconds}`);
  }
  const proto = String(req.headers['x-forwarded-proto'] || '').trim();
  const isSecure = proto === 'https' || requestOrigin(req).startsWith('https://');
  if (isSecure) {
    parts.push('Secure');
  }
  return parts.join('; ');
}

async function loadAuthJsSession(req, authConfig) {
  if (!authConfig) {
    return null;
  }
  const sessionURL = new URL('/auth/session', requestOrigin(req));
  const response = await Auth(toAuthRequest(req, sessionURL.toString(), 'GET'), authConfig);
  if (!response.ok) {
    return null;
  }
  return response.json();
}

async function loadSession(req, authConfig, authSecret) {
  const authSession = await loadAuthJsSession(req, authConfig);
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

  const devUsername = verifyDevSession(parseCookies(req).get(DEV_SESSION_COOKIE), authSecret);
  if (!devUsername) {
    return null;
  }

  return {
    user: {
      name: devUsername,
      username: devUsername,
    },
    expires: '',
    source: 'dev',
  };
}

async function readJSON(req) {
  const chunks = [];
  for await (const chunk of req) {
    chunks.push(Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk));
  }
  const raw = Buffer.concat(chunks).toString('utf8').trim();
  if (!raw) {
    return {};
  }
  return JSON.parse(raw);
}

function escapeHTML(value) {
  return String(value || '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;');
}

function renderDevicePage({ userCode, startupError }) {
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

function createAuthHandler(authConfig, startupError) {
  if (startupError || !authConfig) {
    return async (_req, res) => {
      json(res, 500, { error: startupError || 'Auth is not configured' });
    };
  }
  return async (req, res) => {
    const response = await Auth(toAuthRequest(req, authRouteURL(req)), authConfig);
    await sendResponse(res, response);
  };
}

export function authJsMiddlewarePlugin({ gatewayTarget }) {
  const { authConfig, authSecret, startupError } = createAuthContext();
  const authHandler = createAuthHandler(authConfig, startupError);

  const sessionHandler = async (req, res) => {
    if (!['GET', 'HEAD'].includes(req.method || 'GET')) {
      json(res, 405, { error: 'Method not allowed' });
      return;
    }
    if (startupError || !authSecret) {
      json(res, 500, { error: startupError || 'Auth is not configured' });
      return;
    }
    const session = await loadSession(req, authConfig, authSecret);
    if (!session?.user?.username) {
      json(res, 401, { error: 'Not signed in' });
      return;
    }
    json(res, 200, session);
  };

  const devLoginHandler = async (req, res) => {
    if (req.method !== 'POST') {
      json(res, 405, { error: 'Method not allowed' });
      return;
    }
    if (startupError || !authSecret) {
      json(res, 500, { error: startupError || 'Auth is not configured' });
      return;
    }

    let payload;
    try {
      payload = await readJSON(req);
    } catch {
      json(res, 400, { error: 'Invalid JSON body' });
      return;
    }

    const username = String(payload?.username || '').trim();
    if (!USERNAME_PATTERN.test(username)) {
      json(res, 400, { error: 'Invalid username' });
      return;
    }

    appendSetCookie(res, serializeCookie(req, DEV_SESSION_COOKIE, signDevSession(username, authSecret), 60 * 60 * 24 * 30));
    json(res, 200, {
      user: {
        name: username,
        username,
      },
      source: 'dev',
      expires: '',
    });
  };

  const devLogoutHandler = async (req, res) => {
    if (req.method !== 'POST') {
      json(res, 405, { error: 'Method not allowed' });
      return;
    }
    appendSetCookie(res, serializeCookie(req, DEV_SESSION_COOKIE, '', 0));
    json(res, 200, { ok: true });
  };

  const approveHandler = async (req, res) => {
    if (req.method !== 'POST') {
      json(res, 405, { error: 'Method not allowed' });
      return;
    }
    if (startupError || !authSecret) {
      json(res, 500, { error: startupError || 'Auth is not configured' });
      return;
    }
    const session = await loadSession(req, authConfig, authSecret);
    const username = String(session?.user?.username || '').trim();
    if (!username) {
      json(res, 401, { error: 'Sign in required' });
      return;
    }
    let payload;
    try {
      payload = await readJSON(req);
    } catch {
      json(res, 400, { error: 'Invalid JSON body' });
      return;
    }
    const userCode = String(payload?.userCode || '').trim().toUpperCase();
    if (!userCode) {
      json(res, 400, { error: 'userCode is required' });
      return;
    }

    const response = await fetch(new URL('/v1/auth/device/approve', gatewayTarget), {
      method: 'POST',
      headers: {
        Authorization: `User ${username}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ userCode }),
    });
    const bodyText = await response.text();
    res.statusCode = response.status;
    res.setHeader('Content-Type', response.headers.get('content-type') || 'application/json');
    res.end(bodyText);
  };

  const devicePageHandler = async (req, res) => {
    if (!['GET', 'HEAD'].includes(req.method || 'GET')) {
      json(res, 405, { error: 'Method not allowed' });
      return;
    }
    const pageURL = new URL(req.url || '/', requestOrigin(req));
    const userCode = pageURL.searchParams.get('user_code') || '';
    res.statusCode = 200;
    res.setHeader('Content-Type', 'text/html; charset=utf-8');
    res.end(renderDevicePage({ userCode, startupError }));
  };

  return {
    name: 'authjs-middleware',
    configureServer(server) {
      server.middlewares.use('/auth/dev-login', devLoginHandler);
      server.middlewares.use('/auth/dev-logout', devLogoutHandler);
      server.middlewares.use('/auth/session', sessionHandler);
      server.middlewares.use('/auth/device/approve', approveHandler);
      server.middlewares.use('/auth/device', devicePageHandler);
      server.middlewares.use('/auth', authHandler);
    },
    configurePreviewServer(server) {
      server.middlewares.use('/auth/dev-login', devLoginHandler);
      server.middlewares.use('/auth/dev-logout', devLogoutHandler);
      server.middlewares.use('/auth/session', sessionHandler);
      server.middlewares.use('/auth/device/approve', approveHandler);
      server.middlewares.use('/auth/device', devicePageHandler);
      server.middlewares.use('/auth', authHandler);
    },
  };
}
