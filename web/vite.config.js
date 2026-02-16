import { randomBytes } from 'node:crypto';
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import { Auth } from '@auth/core';
import Google from '@auth/core/providers/google';
import GitHub from '@auth/core/providers/github';

const fileGatewayTarget = process.env.VITE_FILE_API_PROXY_TARGET || 'http://localhost:8080';

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

function authHandler() {
  const authSecret = process.env.AUTH_SECRET;
  const githubScope = (process.env.AUTH_GITHUB_SCOPE || 'read:user user:email').trim();

  if (!authSecret) {
    return async (req, res) => {
      res.statusCode = 500;
      res.setHeader('Content-Type', 'application/json');
      res.end(JSON.stringify({ error: 'AUTH_SECRET is not configured' }));
    };
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
        authorization: {
          params: {
            scope: githubScope,
          },
        },
      }),
    );
  }

  if (providers.length === 0) {
    return async (req, res) => {
      res.statusCode = 500;
      res.setHeader('Content-Type', 'application/json');
      res.end(JSON.stringify({ error: 'No OAuth providers configured' }));
    };
  }

  const authConfig = {
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
  };

  return async (req, res) => {
    const origin = `http://${req.headers.host || 'localhost'}`;
    const url = new URL(req.url || '/', origin);
    const body = ['GET', 'HEAD'].includes(req.method || 'GET') ? undefined : req;

    const request = new Request(url.toString(), {
      method: req.method,
      headers: req.headers,
      body,
      duplex: body ? 'half' : undefined,
    });

    const response = await Auth(request, authConfig);
    res.statusCode = response.status;
    response.headers.forEach((value, key) => {
      res.setHeader(key, value);
    });
    const payload = await response.arrayBuffer();
    res.end(Buffer.from(payload));
  };
}

function authJsMiddlewarePlugin() {
  const handler = authHandler();
  return {
    name: 'authjs-middleware',
    configureServer(server) {
      server.middlewares.use('/auth', handler);
    },
    configurePreviewServer(server) {
      server.middlewares.use('/auth', handler);
    },
  };
}

export default defineConfig({
  plugins: [react(), authJsMiddlewarePlugin()],
  server: {
    proxy: {
      '/v1': {
        target: fileGatewayTarget,
        changeOrigin: true,
      },
    },
  },
  preview: {
    allowedHosts: ['agenttools.dev'],
    proxy: {
      '/v1': {
        target: fileGatewayTarget,
        changeOrigin: true,
      },
    },
  },
});
