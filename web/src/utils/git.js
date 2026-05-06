function normalizeBaseUrl(value) {
  return String(value || '').trim().replace(/\/+$/, '');
}

function encodeGitSlug(slug) {
  return String(slug || '')
    .split('/')
    .filter(Boolean)
    .map(encodeURIComponent)
    .join('/');
}

export function buildGitEndpoint({ slice, publicApiBaseUrl }) {
  const slug = String(slice?.slug || '').trim();
  if (!slug) {
    return '';
  }
  const baseUrl = normalizeBaseUrl(publicApiBaseUrl) || (typeof window !== 'undefined' ? window.location.origin : '');
  if (!baseUrl) {
    return '';
  }
  return `${baseUrl}/git/${encodeGitSlug(slug)}.git`;
}

export function buildGitCloneCommand(gitEndpoint) {
  const endpoint = String(gitEndpoint || '').trim();
  return endpoint ? `git clone ${endpoint}` : '';
}

export function buildSliceCheckoutCommand({ slice, sliceId }) {
  const sliceRef = String(slice?.slug || sliceId || '').trim();
  return sliceRef ? `gs slice checkout ${sliceRef}` : '';
}
