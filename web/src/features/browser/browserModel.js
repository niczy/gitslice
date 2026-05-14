import { normalizeEntryType } from '../../utils/normalize.js';
import {
  IMAGE_EXTENSIONS,
  IMAGE_MIME_TYPES,
} from './browserConstants.js';

export function getFileExtension(filePath) {
  if (!filePath || !filePath.includes('.')) {
    return '';
  }
  return filePath.split('.').pop()?.toLowerCase() || '';
}

export function getEntryName(entry) {
  const name = String(entry?.name || '').trim();
  if (name) {
    return name;
  }
  const path = String(entry?.path || '').replace(/^\/+|\/+$/g, '');
  if (!path) {
    return '/';
  }
  return path.split('/').pop() || path;
}

export function getEntryDisplayPath(entry) {
  const path = String(entry?.path || '').replace(/^\/+/, '');
  return path ? `/${path}` : '/';
}

export function sortEntriesByTypeAndName(entries = []) {
  return [...entries].sort((left, right) => {
    const leftType = normalizeEntryType(left.type);
    const rightType = normalizeEntryType(right.type);
    if (leftType !== rightType) {
      return leftType === 'directory' ? -1 : 1;
    }
    return getEntryName(left).localeCompare(getEntryName(right), undefined, { sensitivity: 'base' });
  });
}

export function getNumericFileSize(value) {
  const size = typeof value === 'number' ? value : Number.parseInt(value, 10);
  return Number.isFinite(size) && size >= 0 ? size : null;
}

export function getFilePayloadSize(filePayload, decodedContent = '') {
  const payloadSize = getNumericFileSize(filePayload?.size);
  if (payloadSize !== null) {
    return payloadSize;
  }
  return decodedContent ? decodedContent.length : null;
}

export function getTreeFileSize(treeEntries, filePath) {
  const normalizedPath = String(filePath || '').replace(/^\/+/, '');
  if (!normalizedPath) {
    return null;
  }
  const parentPath = normalizedPath.includes('/') ? normalizedPath.split('/').slice(0, -1).join('/') : '';
  const entry = (treeEntries?.[parentPath] || []).find((item) => String(item?.path || '').replace(/^\/+/, '') === normalizedPath);
  return normalizeEntryType(entry?.type) === 'file' ? getNumericFileSize(entry?.size) : null;
}

export function getDirectoryAncestorPaths(path) {
  const parts = String(path || '').split('/').filter(Boolean);
  const ancestors = [''];
  for (let index = 0; index < parts.length; index += 1) {
    ancestors.push(parts.slice(0, index + 1).join('/'));
  }
  return ancestors;
}

export function getParentDirectoryPath(path) {
  const parts = String(path || '').split('/').filter(Boolean);
  return parts.slice(0, -1).join('/');
}

export function getPreviewMeta(filePath, encodedContent) {
  const extension = getFileExtension(filePath);
  if (extension === 'pdf') {
    return {
      mode: 'pdf',
      src: `data:application/pdf;base64,${encodedContent}`,
    };
  }

  if (IMAGE_EXTENSIONS.has(extension)) {
    return {
      mode: 'image',
      src: `data:${IMAGE_MIME_TYPES[extension] || 'image/*'};base64,${encodedContent}`,
    };
  }

  if (extension === 'md' || extension === 'markdown') {
    return { mode: 'markdown', src: '' };
  }

  return { mode: 'text', src: '' };
}
