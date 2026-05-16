export function normalizeVisibility(value) {
  if (value === 2 || value === 'VISIBILITY_PUBLIC' || value === 'PUBLIC' || value === 'public') {
    return 'public';
  }
  return 'private';
}

export function visibilityRequestValue(value) {
  return value === 'public' ? 2 : 1;
}

export function visibilityTone(value) {
  return value === 'public' ? 'public' : 'private';
}

export function visibilityLabel(value) {
  return value === 'public' ? 'Public' : 'Private';
}
