export function normalizeWorkspaceResultPath(value) {
  return String(value || '').replace(/^\/+/, '');
}

function encodeBrowserPath(value) {
  return String(value || '').split('/').map(encodeURIComponent).join('/');
}

function buildSliceHashQuery(sliceHash) {
  const params = new URLSearchParams();
  if (sliceHash) {
    params.set('slice_version.slice_hash', sliceHash);
  }
  const queryString = params.toString();
  return queryString ? `?${queryString}` : '';
}

function buildPublicSliceQuery({ sliceId, sliceSlug = '' }) {
  const params = new URLSearchParams();
  if (sliceId) {
    params.set('slice_id', sliceId);
  }
  if (sliceSlug) {
    params.set('slice_slug', sliceSlug);
  }
  const queryString = params.toString();
  return queryString ? `?${queryString}` : '';
}

export function buildBrowserEntriesUrl({
  apiBaseUrl,
  sliceId,
  path,
  sliceHash,
}) {
  const encodedPath = path ? encodeBrowserPath(path) : '';
  const pathSuffix = encodedPath ? `/${encodedPath}` : '';
  return `${apiBaseUrl}/v1/slices/${sliceId}/entries${pathSuffix}${buildSliceHashQuery(sliceHash)}`;
}

export function buildPublicBrowserEntriesUrl({
  apiBaseUrl,
  sliceId,
  path,
  sliceSlug = '',
}) {
  const encodedPath = path ? encodeBrowserPath(path) : '';
  const pathSuffix = encodedPath ? `/${encodedPath}` : '';
  return `${apiBaseUrl}/v1/public/entries${pathSuffix}${buildPublicSliceQuery({ sliceId, sliceSlug })}`;
}

export function buildBrowserFileUrl({
  apiBaseUrl,
  sliceId,
  filePath,
  sliceHash,
}) {
  const encodedPath = filePath ? encodeBrowserPath(filePath) : '';
  const pathSuffix = encodedPath ? `/${encodedPath}` : '';
  return `${apiBaseUrl}/v1/slices/${sliceId}/files${pathSuffix}${buildSliceHashQuery(sliceHash)}`;
}

export function buildPublicBrowserFileUrl({
  apiBaseUrl,
  sliceId,
  filePath,
  sliceSlug = '',
}) {
  const encodedPath = filePath ? encodeBrowserPath(filePath) : '';
  const pathSuffix = encodedPath ? `/${encodedPath}` : '';
  return `${apiBaseUrl}/v1/public/files${pathSuffix}${buildPublicSliceQuery({ sliceId, sliceSlug })}`;
}

export function buildBrowserRawFileUrl({
  sliceId,
  filePath,
  sliceHash,
}) {
  const encodedPath = filePath ? encodeBrowserPath(filePath) : '';
  return `/raw/slices/${encodeURIComponent(sliceId)}/${encodedPath}${buildSliceHashQuery(sliceHash)}`;
}

export function buildPublicBrowserRawFileUrl({
  sliceId,
  filePath,
  sliceSlug = '',
}) {
  const encodedPath = filePath ? encodeBrowserPath(filePath) : '';
  return `/raw/public/${encodedPath}${buildPublicSliceQuery({ sliceId, sliceSlug })}`;
}

export function buildBrowserFileHistoryUrl({
  apiBaseUrl,
  sliceId,
  filePath,
}) {
  const encodedPath = filePath ? encodeBrowserPath(filePath) : '';
  const pathSuffix = encodedPath ? `/${encodedPath}` : '';
  return `${apiBaseUrl}/v1/slices/${sliceId}/files/history${pathSuffix}`;
}

export async function readBrowserErrorMessage(response, fallbackMessage) {
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
  if (!detail) {
    return `${fallbackMessage} (${response.status})`;
  }
  return `${fallbackMessage}: ${detail}`;
}
