export function getSliceDisplayName(sliceNameOrId) {
  const value = (sliceNameOrId || '').trim();
  if (!value) {
    return '';
  }

  const withoutScheme = value.replace(/^https?:\/\//i, '');
  const segments = withoutScheme.split('/').filter(Boolean);
  if (segments.length >= 3 && segments[0].toLowerCase() === 'github.com') {
    return `GitHub.com/${segments.slice(1).join('/')}`;
  }

  if (segments.length >= 2 && !segments[0].includes('.')) {
    return `GitHub.com/${segments.join('/')}`;
  }

  return withoutScheme;
}
