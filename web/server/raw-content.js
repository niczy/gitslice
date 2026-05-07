import { getConfiguredAPIBaseURL, base64ToBytes } from '../shared/runtime.js';
import { clearLocalSessionCookie, getAuthProvider, getProxyAuthorizationResult } from './auth.js';

const TEXT_EXTENSIONS = new Set([
  'bash',
  'c',
  'cc',
  'conf',
  'cpp',
  'cs',
  'css',
  'csv',
  'go',
  'h',
  'hpp',
  'html',
  'java',
  'js',
  'json',
  'jsx',
  'log',
  'md',
  'mjs',
  'proto',
  'py',
  'rb',
  'rs',
  'sh',
  'sql',
  'svg',
  'toml',
  'ts',
  'tsx',
  'txt',
  'xml',
  'yaml',
  'yml',
  'zsh',
]);

function getGatewayTarget() {
  return getConfiguredAPIBaseURL(process.env, 'http://localhost:50051');
}

function normalizeSuffix(value) {
  return String(value || '').replace(/^\/+|\/+$/g, '');
}

function encodePath(rawPath) {
  return normalizeSuffix(rawPath)
    .split('/')
    .filter(Boolean)
    .map(encodeURIComponent)
    .join('/');
}

function decodePathSegment(segment) {
  try {
    return decodeURIComponent(String(segment || ''));
  } catch {
    return String(segment || '');
  }
}

function parseRawTarget(request, suffix) {
  const url = new URL(request.url);
  const parts = normalizeSuffix(suffix).split('/').filter(Boolean);
  if (parts.length === 0) {
    return { error: 'Raw URLs require a file path.' };
  }

  if (parts[0] === 'public') {
    const filePath = parts.slice(1).map(decodePathSegment).join('/');
    const sliceId = String(url.searchParams.get('slice_id') || '').trim();
    const sliceSlug = String(url.searchParams.get('slice_slug') || '').trim();
    if (!filePath) {
      return { error: 'Raw public URLs require a file path.' };
    }
    if (!sliceId && !sliceSlug) {
      return { error: 'Raw public URLs require slice_id or slice_slug.' };
    }
    return {
      mode: 'public',
      filePath,
      sliceId,
      sliceSlug,
    };
  }

  if (parts[0] !== 'slices') {
    return {
      mode: 'root',
      filePath: parts.map(decodePathSegment).join('/'),
    };
  }

  const sliceId = decodePathSegment(parts[1] || '');
  const filePath = parts.slice(2).map(decodePathSegment).join('/');
  if (!sliceId || !filePath) {
    return { error: 'Raw URLs must use /raw/<path>, /raw/slices/<slice-id>/<path>, or /raw/public/<path>?slice_id=<slice-id>.' };
  }
  return {
    mode: 'slice',
    filePath,
    sliceId,
  };
}

function buildTargetURL(pathname, params = new URLSearchParams()) {
  const targetURL = new URL(pathname, getGatewayTarget());
  const query = params.toString();
  if (query) {
    targetURL.search = query;
  }
  return targetURL;
}

function buildAuthenticatedFileURL(rawTarget, requestURL) {
  const params = new URLSearchParams(requestURL.search);
  const encodedPath = encodePath(rawTarget.filePath);
  const suffix = encodedPath ? `/${encodedPath}` : '';
  return buildTargetURL(`/v1/slices/${encodeURIComponent(rawTarget.sliceId)}/files${suffix}`, params);
}

function buildPublicFileURL(rawTarget, requestURL) {
  const params = new URLSearchParams();
  const sliceId = rawTarget.sliceId || requestURL.searchParams.get('slice_id') || '';
  const sliceSlug = rawTarget.sliceSlug || requestURL.searchParams.get('slice_slug') || '';
  if (sliceId) {
    params.set('slice_id', sliceId);
  }
  if (sliceSlug) {
    params.set('slice_slug', sliceSlug);
  }
  const encodedPath = encodePath(rawTarget.filePath);
  const suffix = encodedPath ? `/${encodedPath}` : '';
  return buildTargetURL(`/v1/public/files${suffix}`, params);
}

function buildRootFileURL(rawTarget, requestURL) {
  const params = new URLSearchParams(requestURL.search);
  params.delete('slice_id');
  params.delete('slice_slug');
  const encodedPath = encodePath(rawTarget.filePath);
  const suffix = encodedPath ? `/${encodedPath}` : '';
  return buildTargetURL(`/v1/files${suffix}`, params);
}

async function buildHeadersForUpstream(request, authenticated) {
  const headers = new Headers();
  headers.set('Accept', 'application/json');
  headers.set('x-forwarded-host', new URL(request.url).host);
  headers.set('x-forwarded-proto', new URL(request.url).protocol.replace(':', ''));
  if (request.headers.has('If-None-Match')) {
    headers.set('If-None-Match', request.headers.get('If-None-Match'));
  }

  const responseCookies = [];
  if (!authenticated) {
    return { headers, responseCookies, hasAuthorization: false, rejectUnauthenticated: false };
  }

  if (request.headers.has('Authorization')) {
    headers.set('Authorization', request.headers.get('Authorization'));
    return { headers, responseCookies, hasAuthorization: true, rejectUnauthenticated: false };
  }

  if (getAuthProvider() === 'clerk') {
    const authResult = await getProxyAuthorizationResult(request);
    responseCookies.push(...(authResult.setCookies || []));
    if (authResult.authorization) {
      headers.set('Authorization', authResult.authorization);
      return { headers, responseCookies, hasAuthorization: true, rejectUnauthenticated: false };
    }
    return {
      headers,
      responseCookies,
      hasAuthorization: false,
      rejectUnauthenticated: Boolean(authResult.rejectUnauthenticated),
    };
  }

  return { headers, responseCookies, hasAuthorization: false, rejectUnauthenticated: false };
}

async function fetchJSONFile(request, targetURL, authenticated) {
  const { headers, responseCookies, hasAuthorization, rejectUnauthenticated } = await buildHeadersForUpstream(request, authenticated);
  if (authenticated && !hasAuthorization) {
    return {
      skipped: true,
      responseCookies,
      rejectUnauthenticated,
    };
  }

  try {
    const response = await fetch(targetURL, {
      method: 'GET',
      headers,
      redirect: 'manual',
    });
    if (response.status === 401) {
      const responseText = await response.clone().text();
      if (/invalid session token/i.test(responseText)) {
        responseCookies.push(clearLocalSessionCookie(request));
      }
    }
    return {
      response,
      responseCookies,
    };
  } catch {
    return {
      error: 'Unable to reach upstream API origin',
      status: 502,
      responseCookies,
    };
  }
}

function appendSetCookies(headers, responseCookies) {
  for (const cookie of responseCookies || []) {
    if (cookie) {
      headers.append('Set-Cookie', cookie);
    }
  }
}

function textResponse(message, status, responseCookies = []) {
  const headers = new Headers({
    'Content-Type': 'text/plain; charset=utf-8',
    'X-Content-Type-Options': 'nosniff',
    'Cache-Control': 'no-store',
  });
  appendSetCookies(headers, responseCookies);
  return new Response(message, { status, headers });
}

async function upstreamErrorResponse(response, fallbackMessage, responseCookies = []) {
  let message = fallbackMessage;
  try {
    const text = await response.text();
    if (text) {
      try {
        const payload = JSON.parse(text);
        message = payload?.message || payload?.error || text;
      } catch {
        message = text;
      }
    }
  } catch {
    message = fallbackMessage;
  }
  return textResponse(message, response.status || 502, responseCookies);
}

function formatETag(value) {
  const hash = String(value || '').trim();
  if (!hash) {
    return '';
  }
  if (/^(W\/)?"[^"]+"$/.test(hash)) {
    return hash;
  }
  return `"${hash.replace(/["\\]/g, '')}"`;
}

function isLikelyText(bytes) {
  const sampleSize = Math.min(bytes.length, 512);
  for (let index = 0; index < sampleSize; index += 1) {
    const value = bytes[index];
    if (value === 0) {
      return false;
    }
    if (value < 32 && ![9, 10, 13].includes(value)) {
      return false;
    }
  }
  return true;
}

function contentTypeForPath(filePath, bytes) {
  const extension = String(filePath || '').split('.').pop()?.toLowerCase() || '';
  if (TEXT_EXTENSIONS.has(extension) || isLikelyText(bytes)) {
    return 'text/plain; charset=utf-8';
  }
  return 'application/octet-stream';
}

function contentDispositionForPath(filePath) {
  const filename = String(filePath || '').split('/').filter(Boolean).pop() || 'file';
  const safeFilename = filename.replace(/["\\\r\n]/g, '_');
  return `inline; filename="${safeFilename}"`;
}

function buildRawHeaders({ filePath, bytes, hash, source, responseCookies = [] }) {
  const headers = new Headers({
    'Content-Type': contentTypeForPath(filePath, bytes),
    'Content-Length': String(bytes.byteLength),
    'Content-Disposition': contentDispositionForPath(filePath),
    'X-Content-Type-Options': 'nosniff',
    'Content-Security-Policy': "default-src 'none'; sandbox",
  });
  const etag = formatETag(hash);
  if (etag) {
    headers.set('ETag', etag);
  }
  if (source === 'authenticated') {
    headers.set('Cache-Control', 'private, no-store');
    headers.set('Vary', 'Authorization, Cookie');
  } else {
    headers.set('Cache-Control', 'public, no-cache');
  }
  appendSetCookies(headers, responseCookies);
  return headers;
}

function notModifiedResponse({ hash, source, responseCookies = [] }) {
  const headers = new Headers({
    'X-Content-Type-Options': 'nosniff',
    'Content-Security-Policy': "default-src 'none'; sandbox",
  });
  const etag = formatETag(hash);
  if (etag) {
    headers.set('ETag', etag);
  }
  if (source === 'authenticated') {
    headers.set('Cache-Control', 'private, no-store');
    headers.set('Vary', 'Authorization, Cookie');
  } else {
    headers.set('Cache-Control', 'public, no-cache');
  }
  appendSetCookies(headers, responseCookies);
  return new Response(null, { status: 304, headers });
}

function requestETagMatches(request, etag) {
  const candidate = String(etag || '').trim();
  if (!candidate) {
    return false;
  }
  return String(request.headers.get('If-None-Match') || '')
    .split(',')
    .map((value) => value.trim())
    .some((value) => value === candidate || value === '*');
}

async function rawResponseFromUpstream({ request, response, source, rawTarget, responseCookies }) {
  if (response.status === 304) {
    return notModifiedResponse({
      hash: response.headers.get('ETag') || '',
      source,
      responseCookies,
    });
  }
  if (!response.ok) {
    return upstreamErrorResponse(response, 'Unable to load raw file content', responseCookies);
  }

  let payload;
  try {
    payload = await response.json();
  } catch {
    return textResponse('Upstream returned an invalid file response.', 502, responseCookies);
  }

  const file = payload?.file || {};
  const hash = file.hash || response.headers.get('ETag') || '';
  const etag = formatETag(hash);
  if (requestETagMatches(request, etag)) {
    return notModifiedResponse({ hash: etag, source, responseCookies });
  }

  let bytes;
  try {
    bytes = base64ToBytes(file.content || '');
  } catch {
    return textResponse('Upstream returned invalid file content.', 502, responseCookies);
  }

  const filePath = file.path || rawTarget.filePath;
  const headers = buildRawHeaders({
    filePath,
    bytes,
    hash: etag,
    source,
    responseCookies,
  });
  return new Response(request.method.toUpperCase() === 'HEAD' ? null : bytes, {
    status: 200,
    headers,
  });
}

async function tryAuthenticatedRaw(request, rawTarget, requestURL) {
  const authenticatedURL = buildAuthenticatedFileURL(rawTarget, requestURL);
  const result = await fetchJSONFile(request, authenticatedURL, true);
  if (result.skipped) {
    return {
      skipped: true,
      responseCookies: result.responseCookies,
    };
  }
  if (result.error) {
    return {
      finalResponse: textResponse(result.error, result.status || 502, result.responseCookies),
      responseCookies: result.responseCookies,
    };
  }
  if (result.response.ok || result.response.status === 304) {
    return {
      finalResponse: await rawResponseFromUpstream({
        request,
        response: result.response,
        source: 'authenticated',
        rawTarget,
        responseCookies: result.responseCookies,
      }),
      responseCookies: result.responseCookies,
    };
  }
  if (![401, 403, 404].includes(result.response.status)) {
    return {
      finalResponse: await upstreamErrorResponse(result.response, 'Unable to load raw file content', result.responseCookies),
      responseCookies: result.responseCookies,
    };
  }
  return {
    skipped: true,
    responseCookies: result.responseCookies,
  };
}

async function publicRawResponse(request, rawTarget, requestURL, responseCookies = []) {
  const publicURL = buildPublicFileURL(rawTarget, requestURL);
  const result = await fetchJSONFile(request, publicURL, false);
  const allCookies = [...responseCookies, ...(result.responseCookies || [])];
  if (result.error) {
    return textResponse(result.error, result.status || 502, allCookies);
  }
  return rawResponseFromUpstream({
    request,
    response: result.response,
    source: 'public',
    rawTarget,
    responseCookies: allCookies,
  });
}

async function rootRawResponse(request, rawTarget, requestURL) {
  const rootURL = buildRootFileURL(rawTarget, requestURL);
  const result = await fetchJSONFile(request, rootURL, false);
  if (result.error) {
    return textResponse(result.error, result.status || 502, result.responseCookies);
  }
  return rawResponseFromUpstream({
    request,
    response: result.response,
    source: 'public',
    rawTarget,
    responseCookies: result.responseCookies,
  });
}

export async function handleRawContentRequest(request, suffix = '') {
  const method = request.method.toUpperCase();
  if (!['GET', 'HEAD'].includes(method)) {
    return new Response('Raw file content only supports GET and HEAD.', {
      status: 405,
      headers: {
        Allow: 'GET, HEAD',
        'Content-Type': 'text/plain; charset=utf-8',
        'X-Content-Type-Options': 'nosniff',
      },
    });
  }

  const rawTarget = parseRawTarget(request, suffix);
  if (rawTarget.error) {
    return textResponse(rawTarget.error, 400);
  }

  const requestURL = new URL(request.url);
  if (rawTarget.mode === 'slice') {
    const authenticatedResult = await tryAuthenticatedRaw(request, rawTarget, requestURL);
    if (authenticatedResult.finalResponse) {
      return authenticatedResult.finalResponse;
    }
    return publicRawResponse(request, rawTarget, requestURL, authenticatedResult.responseCookies);
  }
  if (rawTarget.mode === 'root') {
    return rootRawResponse(request, rawTarget, requestURL);
  }

  return publicRawResponse(request, rawTarget, requestURL);
}

export const __test = {
  buildAuthenticatedFileURL,
  buildPublicFileURL,
  buildRootFileURL,
  contentTypeForPath,
  parseRawTarget,
};
