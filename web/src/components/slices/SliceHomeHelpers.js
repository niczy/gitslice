import { normalizeEntryType } from '../../utils/normalize.js';
import { getSliceDisplayName } from '../../utils/slices.js';

export function getSliceName(slice) {
  return getSliceDisplayName(slice?.name || slice?.slice_id || 'Untitled slice');
}

export function getSliceMeta(slice) {
  return slice?.slug || slice?.slice_id || '';
}

export function getSliceUpdatedAt(slice) {
  return slice?.updated_at || slice?.updatedAt || slice?.created_at || slice?.createdAt || 0;
}

export function isHomeSlice(slice, homeSliceId) {
  const sliceId = String(slice?.slice_id || '').trim().toLowerCase();
  const normalizedHomeSliceId = String(homeSliceId || '').trim().toLowerCase();
  return Boolean(normalizedHomeSliceId && sliceId === normalizedHomeSliceId);
}

export function getSliceRouteRef(slice, homeSliceId) {
  if (isHomeSlice(slice, homeSliceId)) {
    return slice?.slug || slice?.name || slice?.slice_id || '';
  }
  return slice?.slice_id || '';
}

export function getSliceVisibility(slice) {
  const value = slice?.visibility ?? slice?.Visibility;
  if (value === 2 || value === 'VISIBILITY_PUBLIC' || value === 'PUBLIC' || value === 'public') {
    return 'public';
  }
  if (value === 1 || value === 'VISIBILITY_PRIVATE' || value === 'PRIVATE' || value === 'private') {
    return 'private';
  }
  return slice?.is_root ? 'public' : 'private';
}

export function sortSlices(slices, homeSliceId) {
  return [...slices].sort((left, right) => {
    const leftIsHome = isHomeSlice(left, homeSliceId);
    const rightIsHome = isHomeSlice(right, homeSliceId);
    if (leftIsHome !== rightIsHome) {
      return leftIsHome ? -1 : 1;
    }
    if (left.is_root !== right.is_root) {
      return left.is_root ? -1 : 1;
    }
    return getSliceName(left).localeCompare(getSliceName(right), undefined, { sensitivity: 'base' });
  });
}

export function cleanFolderPath(value) {
  const trimmed = String(value || '').trim();
  const withoutRoot = trimmed.replace(/^\/+/, '').replace(/\/+$/, '');
  return withoutRoot.split('/').filter(Boolean).join('/');
}

export function getHomeRootPath(username, homeSliceId) {
  const usernameRoot = cleanFolderPath(username);
  if (usernameRoot) {
    return usernameRoot;
  }
  const id = String(homeSliceId || '').trim();
  if (id.toLowerCase().startsWith('home_')) {
    return cleanFolderPath(id.slice('home_'.length));
  }
  return '';
}

export function pathUnderHomeRoot(homeRootPath, relativePath) {
  const root = cleanFolderPath(homeRootPath);
  const relative = cleanFolderPath(relativePath);
  if (!root) {
    return relative;
  }
  return relative ? `${root}/${relative}` : root;
}

export function pathRelativeToHomeRoot(homeRootPath, entryPath) {
  const root = cleanFolderPath(homeRootPath);
  const path = cleanFolderPath(entryPath);
  if (!root) {
    return path;
  }
  if (path === root) {
    return '';
  }
  if (path.startsWith(`${root}/`)) {
    return path.slice(root.length + 1);
  }
  return '';
}

export function validateFolderPath(value) {
  const cleaned = cleanFolderPath(value);
  if (!cleaned) {
    return { path: '', error: 'At least one tracked folder is required.' };
  }
  if (cleaned.includes('\0')) {
    return { path: cleaned, error: 'Folder paths cannot contain null bytes.' };
  }
  const invalidSegment = cleaned.split('/').find((segment) => {
    const normalized = segment.trim();
    return normalized === '' || normalized === '.' || normalized === '..' || normalized === '~';
  });
  if (invalidSegment) {
    return { path: cleaned, error: `Folder path contains an invalid segment: ${invalidSegment}` };
  }
  return { path: cleaned, error: '' };
}

export function getEntryName(entry) {
  return entry?.name || String(entry?.path || '').split('/').filter(Boolean).pop() || entry?.path || '';
}

export function sortDirectoryEntries(entries) {
  return [...(entries || [])]
    .filter((entry) => normalizeEntryType(entry?.type) === 'directory')
    .sort((left, right) => getEntryName(left).localeCompare(getEntryName(right), undefined, { sensitivity: 'base' }));
}
