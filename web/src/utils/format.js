// ---------------------------------------------------------------------------
// Formatting utilities
// ---------------------------------------------------------------------------

export function formatChangeType(value) {
  const type = normalizeChangeType(value);
  return type.charAt(0).toUpperCase() + type.slice(1);
}

export function formatTimestamp(timestamp) {
  if (!timestamp) {
    return 'Unknown date';
  }
  const date = new Date(timestamp * 1000);
  return date.toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

export function formatBytes(value) {
  const numValue = typeof value === 'number' ? value : parseFloat(value);

  if (!numValue || isNaN(numValue)) {
    return '0 B';
  }

  const units = ['B', 'KB', 'MB', 'GB'];
  let size = numValue;
  let unitIndex = 0;
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024;
    unitIndex += 1;
  }
  return `${size.toFixed(size >= 10 || unitIndex === 0 ? 0 : 1)} ${units[unitIndex]}`;
}

// Re-export normalizeChangeType here since formatChangeType depends on it
import { normalizeChangeType } from './normalize.js';
