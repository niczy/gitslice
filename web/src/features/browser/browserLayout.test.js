import assert from 'node:assert/strict';
import test from 'node:test';

import {
  SIDEBAR_WIDTH_DEFAULT,
  SIDEBAR_WIDTH_MAX,
  SIDEBAR_WIDTH_MIN,
} from './browserConstants.js';
import { clampSidebarWidth } from './browserLayout.js';

test('clampSidebarWidth normalizes browser sidebar widths', () => {
  assert.equal(clampSidebarWidth('bad'), SIDEBAR_WIDTH_DEFAULT);
  assert.equal(clampSidebarWidth(SIDEBAR_WIDTH_MIN - 10), SIDEBAR_WIDTH_MIN);
  assert.equal(clampSidebarWidth(SIDEBAR_WIDTH_MAX + 10), SIDEBAR_WIDTH_MAX);
  assert.equal(clampSidebarWidth(321.6), 322);
});
