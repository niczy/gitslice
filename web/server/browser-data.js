import { getConfiguredAPIBaseURL } from '../shared/runtime.js';
import { getAuthProvider, getProxyAuthorizationResult } from './auth.js';
import { normalizeSliceInfo } from '../src/utils/normalize.js';

function getGatewayTarget() {
  return getConfiguredAPIBaseURL(process.env, 'http://localhost:50051');
}

function encodePath(value) {
  return String(value || '').split('/').filter(Boolean).map(encodeURIComponent).join('/');
}

function buildGatewayURL(pathname, request, params = new URLSearchParams()) {
  const target = new URL(pathname, getGatewayTarget());
  const sourceURL = new URL(request.url);
  for (const [key, value] of params.entries()) {
    target.searchParams.set(key, value);
  }
  if (!target.search && sourceURL.search && pathname.startsWith('/v1/slices')) {
    // Browser file/detail requests may carry sliceHash in the current route.
    const sliceHash = sourceURL.searchParams.get('sliceHash');
    if (sliceHash) {
      target.searchParams.set('slice_version.slice_hash', sliceHash);
    }
  }
  return target;
}

async function createGatewayHeaders(request, session) {
  const headers = new Headers({
    Accept: 'application/json',
  });

  const requestAuthorization = request.headers.get('Authorization');
  if (requestAuthorization) {
    headers.set('Authorization', requestAuthorization);
    return { headers, setCookies: [] };
  }

  if (['workos', 'clerk'].includes(getAuthProvider())) {
    const authResult = await getProxyAuthorizationResult(request);
    if (authResult.authorization) {
      headers.set('Authorization', authResult.authorization);
    }
    return { headers, setCookies: authResult.setCookies || [] };
  }

  const username = String(session?.user?.username || '').trim();
  if (username) {
    headers.set('Authorization', `User ${username}`);
  }

  return { headers, setCookies: [] };
}

async function readErrorMessage(response, fallback) {
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

async function fetchJSON(request, session, pathname, params) {
  const { headers, setCookies } = await createGatewayHeaders(request, session);
  const response = await fetch(buildGatewayURL(pathname, request, params), {
    headers,
  });
  if (!response.ok) {
    throw new Error(await readErrorMessage(response, 'Request failed'));
  }
  return {
    payload: await response.json(),
    setCookies,
  };
}

export async function loadBrowserRouteData(request, session, routeInfo) {
  if (routeInfo?.page !== 'browser') {
    return {
      data: null,
      setCookies: [],
    };
  }

  const setCookies = [];
  const data = {
    slices: [],
    slicesError: '',
    selectedSliceId: routeInfo.browserState?.slice || '',
    sliceHash: routeInfo.browserState?.sliceHash || '',
    rootEntries: null,
    rootEntriesError: '',
    selectedFile: routeInfo.browserState?.file || '',
    selectedFilePayload: null,
    selectedFileError: '',
  };

  try {
    const { payload, setCookies: cookies } = await fetchJSON(request, session, '/v1/slices', new URLSearchParams({ limit: '200' }));
    setCookies.push(...cookies);
    data.slices = (payload?.slices || []).map(normalizeSliceInfo);
  } catch (error) {
    data.slicesError = error?.message || 'Unable to load slices.';
  }

  if (!data.selectedSliceId) {
    return { data, setCookies };
  }

  try {
    const params = new URLSearchParams();
    if (routeInfo.browserState?.sliceHash) {
      params.set('slice_version.slice_hash', routeInfo.browserState.sliceHash);
    }
    const { payload, setCookies: cookies } = await fetchJSON(
      request,
      session,
      `/v1/slices/${encodeURIComponent(data.selectedSliceId)}/entries`,
      params,
    );
    setCookies.push(...cookies);
    data.rootEntries = payload?.entries || [];
  } catch (error) {
    data.rootEntriesError = error?.message || 'Unable to load entries.';
  }

  if (!data.selectedFile) {
    return { data, setCookies };
  }

  try {
    const params = new URLSearchParams();
    if (routeInfo.browserState?.sliceHash) {
      params.set('slice_version.slice_hash', routeInfo.browserState.sliceHash);
    }
    const encodedFile = encodePath(data.selectedFile);
    const { payload, setCookies: cookies } = await fetchJSON(
      request,
      session,
      `/v1/slices/${encodeURIComponent(data.selectedSliceId)}/files/${encodedFile}`,
      params,
    );
    setCookies.push(...cookies);
    data.selectedFilePayload = payload?.file || null;
  } catch (error) {
    data.selectedFileError = error?.message || 'Unable to load file content.';
  }

  return { data, setCookies };
}
