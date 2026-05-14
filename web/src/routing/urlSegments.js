export function decodeSegment(value) {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

export function normalizePathname(value) {
  const pathname = String(value || '').trim() || '/';
  if (pathname === '/') {
    return '/';
  }
  return pathname.replace(/\/+$/, '') || '/';
}
