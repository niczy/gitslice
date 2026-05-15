import { buildBrowserPath, buildSliceAgentsPath, buildSliceSettingsPath } from './browserRoutes.js';

export function buildPath(page, commitHash, changesetId = '', browserState) {
  if (page === 'diff' && commitHash) {
    return `/diff/${encodeURIComponent(commitHash)}`;
  }
  if (page === 'changeset' && changesetId) {
    return `/changesets/${encodeURIComponent(changesetId)}`;
  }
  if (page === 'slice-commits' && browserState?.slice) {
    return `/slices/${encodeURIComponent(browserState.slice)}/commits`;
  }
  if (page === 'slice-changesets' && browserState?.slice) {
    return `/slices/${encodeURIComponent(browserState.slice)}/changesets`;
  }
  if (page === 'slice-agents' && browserState?.slice) {
    return buildSliceAgentsPath(browserState);
  }
  if (page === 'slice-settings' && browserState?.slice) {
    return buildSliceSettingsPath(browserState);
  }
  if (page === 'login') {
    return '/login';
  }
  if (page === 'docs') {
    return '/docs';
  }
  if (page === 'profile') {
    return '/profile';
  }
  if (page === 'projects') {
    return '/projects';
  }
  if (page === 'settings') {
    if (browserState?.settingsRunnerId) {
      return `/settings/ci/runners/${encodeURIComponent(browserState.settingsRunnerId)}`;
    }
    if (browserState?.settingsSection) {
      return `/settings/${browserState.settingsSection}`;
    }
    return '/settings';
  }
  if (page === 'admin') {
    return '/admin';
  }
  if (page === 'browser') {
    return buildBrowserPath(browserState);
  }
  return '/';
}
