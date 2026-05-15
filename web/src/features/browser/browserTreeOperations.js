import {
  bytesToBase64,
  utf8ToBytes,
} from '../../../shared/runtime.js';
import { normalizeEntryType } from '../../utils/normalize.js';
import { getEntryName } from './browserModel.js';

export const TREE_DIRECTORY_MARKER_FILE = '.gitslicekeep';
export const TREE_CREATE_VISIBILITY_TIMEOUT_MS = 8000;
export const TREE_CREATE_VISIBILITY_POLL_MS = 250;

export function normalizeTreePath(value) {
  return String(value || '').trim().replace(/^\/+|\/+$/g, '');
}

export function normalizeTreeOperationName(value) {
  const name = String(value || '').trim();
  if (!name) {
    throw new Error('Name is required.');
  }
  if (name === '.' || name === '..') {
    throw new Error('Name cannot be "." or "..".');
  }
  if (name.includes('/') || name.includes('\\')) {
    throw new Error('Use a single file or folder name, not a path.');
  }
  if (name.includes('\0')) {
    throw new Error('Name cannot contain null bytes.');
  }
  return name;
}

export function joinTreePath(parentPath, name) {
  const normalizedParent = normalizeTreePath(parentPath);
  const normalizedName = normalizeTreeOperationName(name);
  return normalizedParent ? `${normalizedParent}/${normalizedName}` : normalizedName;
}

export function getTreeEntryParentPath(path) {
  const parts = normalizeTreePath(path).split('/').filter(Boolean);
  return parts.slice(0, -1).join('/');
}

export function getTreeEntryBaseName(path) {
  const parts = normalizeTreePath(path).split('/').filter(Boolean);
  return parts[parts.length - 1] || '';
}

export function getDirectoryMarkerPath(directoryPath) {
  return joinTreePath(directoryPath, TREE_DIRECTORY_MARKER_FILE);
}

export function isDirectoryMarkerPath(path) {
  return getTreeEntryBaseName(path) === TREE_DIRECTORY_MARKER_FILE;
}

export function filterVisibleTreeEntries(entries = []) {
  return entries.filter((entry) => !isDirectoryMarkerPath(entry?.path || entry?.name || ''));
}

export function getDefaultTreeActionTarget(focusedEntry) {
  const entryKind = normalizeEntryType(focusedEntry?.type);
  if (entryKind === 'directory') {
    return {
      path: normalizeTreePath(focusedEntry?.path),
      type: 'directory',
    };
  }
  return {
    path: getTreeEntryParentPath(focusedEntry?.path || ''),
    type: 'directory',
  };
}

export function pathExistsInEntries(entries = [], targetPath) {
  const normalizedPath = normalizeTreePath(targetPath);
  return entries.some((entry) => normalizeTreePath(entry?.path) === normalizedPath);
}

function sleep(ms) {
  return new Promise((resolve) => {
    globalThis.setTimeout(resolve, ms);
  });
}

export async function waitForCreatedTreeEntry({
  fetchEntries,
  parentPath,
  pollMs = TREE_CREATE_VISIBILITY_POLL_MS,
  sleepFn = sleep,
  targetPath,
  timeoutMs = TREE_CREATE_VISIBILITY_TIMEOUT_MS,
}) {
  if (typeof fetchEntries !== 'function') {
    throw new Error('fetchEntries is required.');
  }

  const deadline = Date.now() + Math.max(0, timeoutMs);
  let lastEntries = [];
  for (;;) {
    lastEntries = await fetchEntries(parentPath);
    if (pathExistsInEntries(lastEntries, targetPath)) {
      return { entries: lastEntries, visible: true };
    }
    if (Date.now() >= deadline) {
      return { entries: lastEntries, visible: false };
    }
    await sleepFn(Math.max(0, pollMs));
  }
}

export function entryLabelForPrompt(entry) {
  const entryKind = normalizeEntryType(entry?.type);
  const name = getEntryName(entry);
  if (entryKind === 'directory') {
    return name === '/' ? 'root folder' : `folder "${name}"`;
  }
  return `file "${name}"`;
}

export function remapChildPathForRename(oldDirectoryPath, newDirectoryPath, childPath) {
  const normalizedOld = normalizeTreePath(oldDirectoryPath);
  const normalizedNew = normalizeTreePath(newDirectoryPath);
  const normalizedChild = normalizeTreePath(childPath);
  if (!normalizedOld) {
    return normalizedNew ? `${normalizedNew}/${normalizedChild}` : normalizedChild;
  }
  if (normalizedChild === normalizedOld) {
    return normalizedNew;
  }
  const oldPrefix = `${normalizedOld}/`;
  if (!normalizedChild.startsWith(oldPrefix)) {
    return normalizedChild;
  }
  const suffix = normalizedChild.slice(oldPrefix.length);
  return normalizedNew ? `${normalizedNew}/${suffix}` : suffix;
}

export function encodeTreeTextContent(value) {
  return bytesToBase64(utf8ToBytes(value));
}

export function normalizeChangesetId(payload) {
  return String(payload?.changesetId || payload?.changeset_id || '').trim();
}

export function getMergeResponseStatus(payload) {
  return payload?.status ?? payload?.mergeStatus ?? payload?.merge_status;
}

export function isSuccessfulMergeResponse(payload) {
  const status = getMergeResponseStatus(payload);
  return status === 0
    || status === '0'
    || status === 'MERGE_STATUS_SUCCESS'
    || status === 'success'
    || status === 'SUCCESS';
}

export function mergeResponseErrorMessage(payload) {
  const message = String(payload?.message || '').trim();
  if (message) {
    return message;
  }
  const status = getMergeResponseStatus(payload);
  return status ? `Merge failed with status ${status}.` : 'Merge failed.';
}

export function isHomeSliceId(sliceId) {
  return String(sliceId || '').trim().toLowerCase().startsWith('home_');
}

export function homeSliceUsername(sliceId) {
  const value = String(sliceId || '').trim();
  return isHomeSliceId(value) ? value.slice('home_'.length) : '';
}

export function normalizeSliceFolderMounts(slice) {
  const mounts = slice?.folder_mounts || slice?.folderMounts || [];
  if (!Array.isArray(mounts)) {
    return [];
  }
  return mounts
    .map((mount) => ({
      alias: normalizeTreePath(mount?.alias),
      sourcePath: normalizeTreePath(mount?.source_path || mount?.sourcePath),
    }))
    .filter((mount) => mount.alias || mount.sourcePath);
}

export function getSliceWriteRoots({ sliceId, currentSlice }) {
  if (currentSlice?.is_root || currentSlice?.isRoot) {
    return [];
  }
  if (isHomeSliceId(sliceId)) {
    const username = homeSliceUsername(sliceId);
    return username ? [username] : [];
  }
  const roots = [];
  for (const mount of normalizeSliceFolderMounts(currentSlice)) {
    if (mount.alias) {
      roots.push(mount.alias);
    }
    if (mount.sourcePath) {
      roots.push(mount.sourcePath);
    }
  }
  return Array.from(new Set(roots)).sort();
}

function isPathBelowRoot(path, root) {
  const normalizedPath = normalizeTreePath(path);
  const normalizedRoot = normalizeTreePath(root);
  return Boolean(normalizedPath && normalizedRoot && normalizedPath.startsWith(`${normalizedRoot}/`));
}

function isParentInsideRoot(parentPath, root) {
  const normalizedParent = normalizeTreePath(parentPath);
  const normalizedRoot = normalizeTreePath(root);
  return Boolean(
    normalizedParent
    && normalizedRoot
    && (normalizedParent === normalizedRoot || normalizedParent.startsWith(`${normalizedRoot}/`)),
  );
}

export function getTreeCreateBlockedReason({ sliceId, currentSlice, parentPath }) {
  if (currentSlice?.is_root || currentSlice?.isRoot) {
    return '';
  }

  const normalizedParent = normalizeTreePath(parentPath);
  if (isHomeSliceId(sliceId)) {
    if (!normalizedParent) {
      return 'Open a folder in the home slice before creating files or folders.';
    }
    const roots = getSliceWriteRoots({ sliceId, currentSlice });
    return roots.some((root) => isParentInsideRoot(normalizedParent, root))
      ? ''
      : 'Create files and folders inside the home directory tree.';
  }

  const roots = getSliceWriteRoots({ sliceId, currentSlice });
  if (roots.length === 0) {
    return 'Create files and folders inside one of this slice\'s tracked folders.';
  }
  if (!normalizedParent) {
    return 'Open a tracked folder before creating files or folders in this slice.';
  }
  return roots.some((root) => isParentInsideRoot(normalizedParent, root))
    ? ''
    : 'Create files and folders inside one of this slice\'s tracked folders.';
}

export function assertCanCreateUnderTreePath({ sliceId, currentSlice, parentPath }) {
  const reason = getTreeCreateBlockedReason({ sliceId, currentSlice, parentPath });
  if (reason) {
    throw new Error(reason);
  }
}

export function assertTreeMutationPathsAllowed({ sliceId, currentSlice, paths }) {
  if (currentSlice?.is_root || currentSlice?.isRoot) {
    return;
  }
  const roots = getSliceWriteRoots({ sliceId, currentSlice });
  if (roots.length === 0) {
    return;
  }
  const disallowedPath = (paths || [])
    .map(normalizeTreePath)
    .filter(Boolean)
    .find((path) => !roots.some((root) => isPathBelowRoot(path, root)));
  if (!disallowedPath) {
    return;
  }
  if (isHomeSliceId(sliceId)) {
    throw new Error('Home slice root cannot contain new files or folders. Open an existing folder first.');
  }
  throw new Error('This path is outside the slice tracked folders.');
}
