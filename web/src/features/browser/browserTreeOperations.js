import {
  bytesToBase64,
  utf8ToBytes,
} from '../../../shared/runtime.js';
import { normalizeEntryType } from '../../utils/normalize.js';
import { getEntryName } from './browserModel.js';

export const TREE_DIRECTORY_MARKER_FILE = '.gitslicekeep';

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
  const normalizedParent = String(parentPath || '').replace(/^\/+|\/+$/g, '');
  const normalizedName = normalizeTreeOperationName(name);
  return normalizedParent ? `${normalizedParent}/${normalizedName}` : normalizedName;
}

export function getTreeEntryParentPath(path) {
  const parts = String(path || '').replace(/^\/+|\/+$/g, '').split('/').filter(Boolean);
  return parts.slice(0, -1).join('/');
}

export function getTreeEntryBaseName(path) {
  const parts = String(path || '').replace(/^\/+|\/+$/g, '').split('/').filter(Boolean);
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
      path: String(focusedEntry?.path || '').replace(/^\/+|\/+$/g, ''),
      type: 'directory',
    };
  }
  return {
    path: getTreeEntryParentPath(focusedEntry?.path || ''),
    type: 'directory',
  };
}

export function pathExistsInEntries(entries = [], targetPath) {
  const normalizedPath = String(targetPath || '').replace(/^\/+|\/+$/g, '');
  return entries.some((entry) => String(entry?.path || '').replace(/^\/+|\/+$/g, '') === normalizedPath);
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
  const normalizedOld = String(oldDirectoryPath || '').replace(/^\/+|\/+$/g, '');
  const normalizedNew = String(newDirectoryPath || '').replace(/^\/+|\/+$/g, '');
  const normalizedChild = String(childPath || '').replace(/^\/+|\/+$/g, '');
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
