import { createRequestHandler } from '@react-router/cloudflare';
import * as build from './build/server/index.js';

function ensureProcessEnv(env) {
  const processLike = globalThis.process && typeof globalThis.process === 'object'
    ? globalThis.process
    : (globalThis.process = {});
  if (!processLike.env || typeof processLike.env !== 'object') {
    processLike.env = {};
  }

  Object.assign(processLike.env, {
    DEPLOY_ENV: String(env?.DEPLOY_ENV || processLike.env.DEPLOY_ENV || 'staging'),
    NODE_ENV: String(env?.NODE_ENV || processLike.env.NODE_ENV || 'production'),
    WEB_DEPLOY_TARGET: 'cloudflare_worker',
    WEB_COMPAT_RUNTIME: 'worker',
    AUTH_PROVIDER: String(env?.AUTH_PROVIDER || processLike.env.AUTH_PROVIDER || 'local'),
    ALLOW_DEV_LOGIN: String(env?.ALLOW_DEV_LOGIN || processLike.env.ALLOW_DEV_LOGIN || ''),
    PUBLIC_WEB_BASE_URL: String(env?.PUBLIC_WEB_BASE_URL || processLike.env.PUBLIC_WEB_BASE_URL || ''),
    PUBLIC_API_BASE_URL: String(env?.PUBLIC_API_BASE_URL || processLike.env.PUBLIC_API_BASE_URL || ''),
    VITE_FILE_API_BASE_URL: String(env?.VITE_FILE_API_BASE_URL || processLike.env.VITE_FILE_API_BASE_URL || ''),
    VITE_FILE_API_PROXY_TARGET: String(
      env?.VITE_FILE_API_PROXY_TARGET ||
        env?.PUBLIC_API_BASE_URL ||
        processLike.env.VITE_FILE_API_PROXY_TARGET ||
        '',
    ),
    AUTH_SECRET: String(env?.AUTH_SECRET || processLike.env.AUTH_SECRET || ''),
    AUTH_GOOGLE_ID: String(env?.AUTH_GOOGLE_ID || processLike.env.AUTH_GOOGLE_ID || ''),
    AUTH_GOOGLE_SECRET: String(env?.AUTH_GOOGLE_SECRET || processLike.env.AUTH_GOOGLE_SECRET || ''),
    AUTH_GITHUB_ID: String(env?.AUTH_GITHUB_ID || processLike.env.AUTH_GITHUB_ID || ''),
    AUTH_GITHUB_SECRET: String(env?.AUTH_GITHUB_SECRET || processLike.env.AUTH_GITHUB_SECRET || ''),
    WORKOS_CLIENT_ID: String(env?.WORKOS_CLIENT_ID || processLike.env.WORKOS_CLIENT_ID || ''),
    WORKOS_API_KEY: String(env?.WORKOS_API_KEY || processLike.env.WORKOS_API_KEY || ''),
    WORKOS_REDIRECT_URI: String(env?.WORKOS_REDIRECT_URI || processLike.env.WORKOS_REDIRECT_URI || ''),
    WORKOS_JWKS_URL: String(env?.WORKOS_JWKS_URL || processLike.env.WORKOS_JWKS_URL || ''),
    WORKOS_COOKIE_PASSWORD: String(env?.WORKOS_COOKIE_PASSWORD || processLike.env.WORKOS_COOKIE_PASSWORD || ''),
    WORKOS_AUTHKIT_DOMAIN: String(env?.WORKOS_AUTHKIT_DOMAIN || processLike.env.WORKOS_AUTHKIT_DOMAIN || ''),
  });
}

const handleAppRequest = createRequestHandler({
  build,
  mode: process.env.NODE_ENV || 'production',
});

function toMutableRequest(request, { stripIfNoneMatch = false } = {}) {
  const headers = new Headers();
  request.headers.forEach((value, key) => {
    if (stripIfNoneMatch && key.toLowerCase() === 'if-none-match') {
      return;
    }
    headers.set(key, value);
  });
  return new Request(request.url, {
    method: request.method,
    headers,
    body: ['GET', 'HEAD'].includes(request.method.toUpperCase()) ? undefined : request.body,
    redirect: request.redirect,
  });
}

async function tryServeAsset(request, env) {
  if (typeof env?.ASSETS?.fetch !== 'function') {
    return null;
  }
  const method = String(request.method || 'GET').toUpperCase();
  if (method !== 'GET' && method !== 'HEAD') {
    return null;
  }
  try {
    const response = await env.ASSETS.fetch(request.url, toMutableRequest(request, { stripIfNoneMatch: true }));
    if (response && response.status >= 200 && response.status < 400) {
      return new Response(response.body, response);
    }
  } catch {
    // Fall through to the app handler.
  }
  return null;
}

export default {
  async fetch(request, env, ctx) {
    ensureProcessEnv(env);
    const assetResponse = await tryServeAsset(request, env);
    if (assetResponse) {
      return assetResponse;
    }

    const appRequest = toMutableRequest(request);
    return handleAppRequest({
      request: appRequest,
      env,
      waitUntil: ctx.waitUntil.bind(ctx),
      passThroughOnException: ctx.passThroughOnException
        ? ctx.passThroughOnException.bind(ctx)
        : () => {},
      params: {},
      data: {},
    });
  },
};
