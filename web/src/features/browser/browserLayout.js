import {
  SIDEBAR_WIDTH_DEFAULT,
  SIDEBAR_WIDTH_MAX,
  SIDEBAR_WIDTH_MIN,
} from './browserConstants.js';

export function clampSidebarWidth(value) {
  const numericValue = Number(value);
  if (!Number.isFinite(numericValue)) {
    return SIDEBAR_WIDTH_DEFAULT;
  }
  return Math.min(SIDEBAR_WIDTH_MAX, Math.max(SIDEBAR_WIDTH_MIN, Math.round(numericValue)));
}
