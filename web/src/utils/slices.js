export function getSliceDisplayName(sliceNameOrId) {
  const value = (sliceNameOrId || '').trim();
  if (!value) {
    return '';
  }

  return value
    .replace(/^https?:\/\//i, '')
    .replace(/\/+$/, '');
}
