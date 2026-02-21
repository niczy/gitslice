export function getSliceDisplayName(sliceNameOrId) {
  const value = (sliceNameOrId || '').trim();
  if (!value) {
    return '';
  }

  const segments = value.split('/').filter(Boolean);
  return segments.length > 0 ? segments[segments.length - 1] : value;
}
