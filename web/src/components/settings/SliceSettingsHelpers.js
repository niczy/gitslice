export function normalizeVisibility(value) {
  if (value === 2 || value === 'VISIBILITY_PUBLIC' || value === 'PUBLIC' || value === 'public') {
    return 'public';
  }
  return 'private';
}

export function normalizePathPropagationMode(value) {
  if (
    value === 2 ||
    value === 'PATH_VISIBILITY_PROPAGATION_MODE_PUBLIC' ||
    value === 'PUBLIC' ||
    value === 'public'
  ) {
    return 'public';
  }
  if (
    value === 3 ||
    value === 'PATH_VISIBILITY_PROPAGATION_MODE_PRIVATE' ||
    value === 'PRIVATE' ||
    value === 'private'
  ) {
    return 'private';
  }
  return 'unchanged';
}

export function visibilityRequestValue(value) {
  return value === 'public' ? 2 : 1;
}

export function pathPropagationRequestValue(value) {
  if (value === 'public') {
    return 2;
  }
  if (value === 'private') {
    return 3;
  }
  return 1;
}

export function visibilityTone(value) {
  return value === 'public' ? 'public' : 'private';
}

export function visibilityLabel(value) {
  return value === 'public' ? 'Public' : 'Private';
}
