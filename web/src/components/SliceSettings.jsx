import { useEffect, useMemo, useState } from 'react';
import {
  fetchEnvironments,
  getPathVisibility,
  getSliceEnvironment,
  getSliceVisibility,
  updatePathVisibility,
  updateSliceEnvironment,
  updateSliceVisibility,
} from '../utils/api.js';
import { Badge } from './ui/badge.jsx';
import { Button } from './ui/button.jsx';
import { Card, CardContent } from './ui/card.jsx';

function normalizeVisibility(value) {
  if (value === 2 || value === 'VISIBILITY_PUBLIC' || value === 'PUBLIC' || value === 'public') {
    return 'public';
  }
  return 'private';
}

function normalizePathPropagationMode(value) {
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

function visibilityRequestValue(value) {
  return value === 'public' ? 2 : 1;
}

function pathPropagationRequestValue(value) {
  if (value === 'public') {
    return 2;
  }
  if (value === 'private') {
    return 3;
  }
  return 1;
}

function normalizePathVisibilityInfo(payload) {
  const visibility = payload?.visibility || payload || {};
  return {
    path: visibility.path || '',
    visibility: normalizeVisibility(visibility.visibility),
    explicitRule: Boolean(visibility.explicit_rule ?? visibility.explicitRule),
    resolvedFromPath: visibility.resolved_from_path ?? visibility.resolvedFromPath ?? '',
    effectiveVisibility: normalizeVisibility(visibility.effective_visibility ?? visibility.effectiveVisibility),
  };
}

function visibilityTone(value) {
  return value === 'public' ? 'public' : 'private';
}

function visibilityLabel(value) {
  return value === 'public' ? 'Public' : 'Private';
}

function visibilitySummary(info) {
  if (!info) {
    return 'No visibility data loaded yet.';
  }
  if (info.explicitRule) {
    return `Explicit ${visibilityLabel(info.visibility).toLowerCase()} rule on this path.`;
  }
  if (info.resolvedFromPath) {
    return `Inherited from ${info.resolvedFromPath}.`;
  }
  return 'No explicit rule. This path is private by default.';
}

function encodePublicPath(rawPath) {
  return String(rawPath || '')
    .replace(/^\/+/, '')
    .split('/')
    .filter(Boolean)
    .map(encodeURIComponent)
    .join('/');
}

function buildPublicUrl(sliceId, path, entryType) {
  if (typeof window === 'undefined' || !sliceId) {
    return '';
  }
  const params = new URLSearchParams({ slice_id: sliceId });
  const encodedPath = encodePublicPath(path);
  const isDirectory = entryType === 'directory';
  const basePath = isDirectory ? '/v1/public/entries' : '/v1/public/files';
  const suffix = encodedPath ? `/${encodedPath}` : '';
  return `${window.location.origin}${basePath}${suffix}?${params.toString()}`;
}

async function copyToClipboard(text) {
  if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  if (typeof document === 'undefined') {
    throw new Error('Clipboard is not available in this environment.');
  }
  const textarea = document.createElement('textarea');
  textarea.value = text;
  textarea.setAttribute('readonly', '');
  textarea.style.position = 'absolute';
  textarea.style.left = '-9999px';
  document.body.appendChild(textarea);
  textarea.select();
  document.execCommand('copy');
  document.body.removeChild(textarea);
}

export default function SliceSettings({ sliceId, sliceName, selectedPath = '', selectedPathType = 'directory' }) {
  const [environments, setEnvironments] = useState([]);
  const [currentEnvironment, setCurrentEnvironment] = useState('');
  const [selectedEnvironment, setSelectedEnvironment] = useState('');
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');

  const [sliceVisibility, setSliceVisibility] = useState('private');
  const [slicePropagationMode, setSlicePropagationMode] = useState('unchanged');
  const [sliceVisibilityLoading, setSliceVisibilityLoading] = useState(true);
  const [sliceVisibilitySaving, setSliceVisibilitySaving] = useState(false);
  const [sliceVisibilityError, setSliceVisibilityError] = useState('');
  const [sliceVisibilitySuccess, setSliceVisibilitySuccess] = useState('');

  const [pathVisibility, setPathVisibility] = useState(null);
  const [pathVisibilityLoading, setPathVisibilityLoading] = useState(false);
  const [pathVisibilitySaving, setPathVisibilitySaving] = useState(false);
  const [pathVisibilityError, setPathVisibilityError] = useState('');
  const [pathVisibilitySuccess, setPathVisibilitySuccess] = useState('');

  const [copiedTarget, setCopiedTarget] = useState('');
  const [copyError, setCopyError] = useState('');

  const hasSelectedPath = Boolean(selectedPath);
  const selectedPathIsDirectory = selectedPathType === 'directory';
  const normalizedMutationPath = useMemo(() => {
    if (!selectedPath) {
      return '';
    }
    return selectedPath.startsWith('/') ? selectedPath : `/${selectedPath}`;
  }, [selectedPath]);
  const slicePublicUrl = useMemo(() => buildPublicUrl(sliceId, '', 'directory'), [sliceId]);
  const visibilityLookupPath = useMemo(() => {
    if (!selectedPath) {
      return '';
    }
    return sliceId?.startsWith('home.') ? normalizedMutationPath : selectedPath;
  }, [normalizedMutationPath, selectedPath, sliceId]);
  const selectedPathPublicUrl = useMemo(() => {
    if (!selectedPath) {
      return '';
    }
    return buildPublicUrl(sliceId, selectedPath, selectedPathType);
  }, [selectedPath, selectedPathType, sliceId]);

  async function refreshSelectedPathVisibility() {
    if (!sliceId || !hasSelectedPath || !visibilityLookupPath) {
      return;
    }
    const response = await getPathVisibility({
      workspaceId: sliceId,
      path: visibilityLookupPath,
    });
    setPathVisibility(normalizePathVisibilityInfo(response));
  }

  useEffect(() => {
    if (!sliceId) {
      setEnvironments([]);
      setCurrentEnvironment('');
      setSelectedEnvironment('');
      setLoading(false);
      return;
    }

    let active = true;

    const load = async () => {
      setLoading(true);
      setError('');
      setSuccess('');
      try {
        const [envList, current] = await Promise.all([
          fetchEnvironments(),
          getSliceEnvironment(sliceId),
        ]);
        if (!active) {
          return;
        }

        const nextEnvironment = current?.environment || '';
        setEnvironments(envList || []);
        setCurrentEnvironment(nextEnvironment);
        setSelectedEnvironment(nextEnvironment);
      } catch (err) {
        if (!active) {
          return;
        }
        setError(err?.message || 'Unable to load slice settings.');
      } finally {
        if (active) {
          setLoading(false);
        }
      }
    };

    load();
    return () => {
      active = false;
    };
  }, [sliceId]);

  useEffect(() => {
    if (!sliceId) {
      setSliceVisibility('private');
      setSlicePropagationMode('unchanged');
      setSliceVisibilityLoading(false);
      setSliceVisibilityError('');
      setSliceVisibilitySuccess('');
      return;
    }

    let active = true;

    const load = async () => {
      setSliceVisibilityLoading(true);
      setSliceVisibilityError('');
      setSliceVisibilitySuccess('');
      try {
        const response = await getSliceVisibility(sliceId);
        if (!active) {
          return;
        }
        setSliceVisibility(normalizeVisibility(response?.visibility));
      } catch (err) {
        if (!active) {
          return;
        }
        setSliceVisibilityError(err?.message || 'Unable to load slice visibility.');
      } finally {
        if (active) {
          setSliceVisibilityLoading(false);
        }
      }
    };

    load();
    return () => {
      active = false;
    };
  }, [sliceId]);

  useEffect(() => {
    if (!sliceId || !hasSelectedPath) {
      setPathVisibility(null);
      setPathVisibilityLoading(false);
      setPathVisibilityError('');
      setPathVisibilitySuccess('');
      return;
    }

    let active = true;

    const load = async () => {
      setPathVisibilityLoading(true);
      setPathVisibilityError('');
      setPathVisibilitySuccess('');
      try {
        const response = await getPathVisibility({
          workspaceId: sliceId,
          path: visibilityLookupPath,
        });
        if (!active) {
          return;
        }
        setPathVisibility(normalizePathVisibilityInfo(response));
      } catch (err) {
        if (!active) {
          return;
        }
        setPathVisibility(null);
        setPathVisibilityError(err?.message || 'Unable to load selected path visibility.');
      } finally {
        if (active) {
          setPathVisibilityLoading(false);
        }
      }
    };

    load();
    return () => {
      active = false;
    };
  }, [hasSelectedPath, sliceId, visibilityLookupPath]);

  useEffect(() => {
    if (!copiedTarget && !copyError) {
      return undefined;
    }
    const timer = window.setTimeout(() => {
      setCopiedTarget('');
      setCopyError('');
    }, 2200);
    return () => window.clearTimeout(timer);
  }, [copiedTarget, copyError]);

  const sortedEnvironments = useMemo(() => {
    return [...environments].sort((a, b) => {
      const aLabel = (a.displayName || a.name || '').toLowerCase();
      const bLabel = (b.displayName || b.name || '').toLowerCase();
      return aLabel.localeCompare(bLabel);
    });
  }, [environments]);

  const canSave = selectedEnvironment !== '' && selectedEnvironment !== currentEnvironment && !saving;

  const saveEnvironment = async () => {
    if (!sliceId || !canSave) {
      return;
    }

    setSaving(true);
    setError('');
    setSuccess('');
    try {
      const updated = await updateSliceEnvironment(sliceId, selectedEnvironment);
      const nextEnvironment = updated?.environment || selectedEnvironment;
      setCurrentEnvironment(nextEnvironment);
      setSelectedEnvironment(nextEnvironment);
      setSuccess('Environment updated. To persist in your repo, update `.gitslice/config.yaml`.');
    } catch (err) {
      setError(err?.message || 'Unable to save slice environment.');
    } finally {
      setSaving(false);
    }
  };

  const saveSliceVisibility = async (nextVisibility) => {
    if (!sliceId || sliceVisibilitySaving) {
      return;
    }

    const nextPropagationMode = nextVisibility === 'public' ? slicePropagationMode : 'unchanged';
    setSliceVisibilitySaving(true);
    setSliceVisibilityError('');
    setSliceVisibilitySuccess('');
    try {
      const response = await updateSliceVisibility(sliceId, {
        visibility: visibilityRequestValue(nextVisibility),
        pathPropagationMode: pathPropagationRequestValue(nextPropagationMode),
      });
      setSliceVisibility(normalizeVisibility(response?.visibility));
      setSlicePropagationMode(normalizePathPropagationMode(response?.path_propagation_mode ?? response?.pathPropagationMode));
      try {
        await refreshSelectedPathVisibility();
      } catch (refreshErr) {
        setPathVisibilityError(refreshErr?.message || 'Unable to refresh selected path visibility.');
      }
      setSliceVisibilitySuccess(`Slice visibility is now ${nextVisibility}.`);
    } catch (err) {
      setSliceVisibilityError(err?.message || 'Unable to update slice visibility.');
    } finally {
      setSliceVisibilitySaving(false);
    }
  };

  const savePathVisibility = async (nextVisibility) => {
    if (!normalizedMutationPath || pathVisibilitySaving) {
      return;
    }

    setPathVisibilitySaving(true);
    setPathVisibilityError('');
    setPathVisibilitySuccess('');
    try {
      const response = await updatePathVisibility({
        path: normalizedMutationPath,
        visibility: visibilityRequestValue(nextVisibility),
        recursive: selectedPathIsDirectory,
      });
      setPathVisibility(normalizePathVisibilityInfo(response));
      setPathVisibilitySuccess(`${selectedPathIsDirectory ? 'Folder' : 'File'} visibility is now ${nextVisibility}.`);
    } catch (err) {
      setPathVisibilityError(err?.message || 'Unable to update path visibility.');
    } finally {
      setPathVisibilitySaving(false);
    }
  };

  const copyUrl = async (key, url) => {
    if (!url) {
      return;
    }
    try {
      await copyToClipboard(url);
      setCopiedTarget(key);
      setCopyError('');
    } catch (err) {
      setCopiedTarget('');
      setCopyError(err?.message || 'Unable to copy link.');
    }
  };

  const currentPathTitle = hasSelectedPath ? (pathVisibility?.path || selectedPath) : 'Choose a file or folder in the tree';

  return (
    <div className="slice-settings" data-testid="slice-settings-panel">
      <div className="slice-settings-header">
        <h3>Slice settings</h3>
        <p>
          Manage runtime defaults, visibility, and public links for <strong>{sliceName || sliceId}</strong>.
        </p>
      </div>

      <div className="slice-settings-grid">
        <Card className="border-border/70">
          <CardContent className="pt-6">
            <div className="slice-settings-card-header">
              <div>
                <h4>Slice visibility</h4>
                <p>Private by default. Making the slice public allows anonymous readers to browse this slice.</p>
              </div>
              <Badge
                variant="outline"
                className={`visibility-badge visibility-badge--${visibilityTone(sliceVisibility)}`}
                data-testid="slice-visibility-status"
              >
                {visibilityLabel(sliceVisibility)}
              </Badge>
            </div>

            {sliceVisibilityLoading && <div className="panel-empty">Loading slice visibility…</div>}
            {!sliceVisibilityLoading && sliceVisibilityError && <div className="panel-error">{sliceVisibilityError}</div>}
            {!sliceVisibilityLoading && sliceVisibilitySuccess && <div className="panel-success">{sliceVisibilitySuccess}</div>}

            {!sliceVisibilityLoading && !sliceVisibilityError && (
              <div className="visibility-stack" data-testid="slice-visibility-panel">
                <div className="visibility-controls">
                  <label className="visibility-field">
                    <span>Path propagation when making the slice public</span>
                    <select
                      value={slicePropagationMode}
                      onChange={(event) => setSlicePropagationMode(event.target.value)}
                      data-testid="slice-visibility-propagation"
                    >
                      <option value="unchanged">Leave existing path rules unchanged</option>
                      <option value="public">Mark current slice paths public</option>
                      <option value="private">Mark current slice paths private</option>
                    </select>
                  </label>
                  <div className="visibility-actions">
                    <Button
                      type="button"
                      variant={sliceVisibility === 'private' ? 'secondary' : 'outline'}
                      disabled={sliceVisibilitySaving}
                      onClick={() => saveSliceVisibility('private')}
                      data-testid="slice-visibility-set-private"
                    >
                      {sliceVisibilitySaving && sliceVisibility === 'private' ? 'Saving…' : 'Make private'}
                    </Button>
                    <Button
                      type="button"
                      variant={sliceVisibility === 'public' ? 'secondary' : 'default'}
                      disabled={sliceVisibilitySaving}
                      onClick={() => saveSliceVisibility('public')}
                      data-testid="slice-visibility-set-public"
                    >
                      {sliceVisibilitySaving && sliceVisibility === 'public' ? 'Saving…' : 'Make public'}
                    </Button>
                  </div>
                </div>

                <div className="visibility-link-block">
                  <label className="visibility-field">
                    <span>Public slice URL</span>
                    <input
                      readOnly
                      value={slicePublicUrl}
                      data-testid="slice-visibility-url"
                    />
                  </label>
                  <div className="visibility-link-actions">
                    <Button
                      type="button"
                      variant="outline"
                      onClick={() => copyUrl('slice', slicePublicUrl)}
                      data-testid="slice-visibility-copy-url"
                    >
                      {copiedTarget === 'slice' ? 'Copied' : 'Copy public URL'}
                    </Button>
                    <span className="slice-settings-note">
                      Anonymous readers only see content that resolves public inside this slice.
                    </span>
                  </div>
                </div>
              </div>
            )}
          </CardContent>
        </Card>

        <Card className="border-border/70">
          <CardContent className="pt-6">
            <div className="slice-settings-card-header">
              <div>
                <h4>Selected path visibility</h4>
                <p>Adjust the file or folder currently highlighted by your browser context.</p>
              </div>
              {hasSelectedPath && pathVisibility && (
                <Badge
                  variant="outline"
                  className={`visibility-badge visibility-badge--${visibilityTone(pathVisibility.effectiveVisibility)}`}
                  data-testid="path-visibility-status"
                >
                  {visibilityLabel(pathVisibility.effectiveVisibility)}
                </Badge>
              )}
            </div>

            {!hasSelectedPath && (
              <div className="panel-empty" data-testid="path-visibility-empty">
                Choose a file or folder from the tree, then open Settings to manage its public visibility.
              </div>
            )}

            {hasSelectedPath && pathVisibilityLoading && <div className="panel-empty">Loading path visibility…</div>}
            {hasSelectedPath && !pathVisibilityLoading && pathVisibilityError && <div className="panel-error">{pathVisibilityError}</div>}
            {hasSelectedPath && !pathVisibilityLoading && pathVisibilitySuccess && <div className="panel-success">{pathVisibilitySuccess}</div>}

            {hasSelectedPath && !pathVisibilityLoading && !pathVisibilityError && pathVisibility && (
              <div className="visibility-stack" data-testid="path-visibility-panel">
                <div className="visibility-target">
                  <div className="visibility-target-meta">
                    <span className="visibility-target-label">{selectedPathIsDirectory ? 'Folder' : 'File'}</span>
                    <code>{currentPathTitle}</code>
                  </div>
                  <div className="visibility-rule-copy">
                    <Badge variant="outline" className="visibility-source-badge">
                      {pathVisibility.explicitRule ? 'Explicit rule' : (pathVisibility.resolvedFromPath ? 'Inherited rule' : 'Default private')}
                    </Badge>
                    <span className="slice-settings-note">{visibilitySummary(pathVisibility)}</span>
                  </div>
                </div>

                <div className="visibility-grid">
                  <div className="visibility-stat">
                    <span className="visibility-stat-label">Rule</span>
                    <strong>{visibilityLabel(pathVisibility.visibility)}</strong>
                  </div>
                  <div className="visibility-stat">
                    <span className="visibility-stat-label">Effective</span>
                    <strong>{visibilityLabel(pathVisibility.effectiveVisibility)}</strong>
                  </div>
                  <div className="visibility-stat">
                    <span className="visibility-stat-label">Source</span>
                    <strong>{pathVisibility.resolvedFromPath || 'default private'}</strong>
                  </div>
                </div>

                <div className="visibility-actions">
                  <Button
                    type="button"
                    variant={pathVisibility.visibility === 'private' ? 'secondary' : 'outline'}
                    disabled={pathVisibilitySaving}
                    onClick={() => savePathVisibility('private')}
                    data-testid="path-visibility-set-private"
                  >
                    {pathVisibilitySaving && pathVisibility.visibility === 'private' ? 'Saving…' : 'Make private'}
                  </Button>
                  <Button
                    type="button"
                    variant={pathVisibility.visibility === 'public' ? 'secondary' : 'default'}
                    disabled={pathVisibilitySaving}
                    onClick={() => savePathVisibility('public')}
                    data-testid="path-visibility-set-public"
                  >
                    {pathVisibilitySaving && pathVisibility.visibility === 'public' ? 'Saving…' : 'Make public'}
                  </Button>
                  {selectedPathIsDirectory && <Badge variant="secondary" className="visibility-recursive-badge">Recursive folder rule</Badge>}
                </div>

                <div className="visibility-link-block">
                  <label className="visibility-field">
                    <span>Public URL for this {selectedPathIsDirectory ? 'folder' : 'file'}</span>
                    <input
                      readOnly
                      value={selectedPathPublicUrl}
                      data-testid="path-visibility-url"
                    />
                  </label>
                  <div className="visibility-link-actions">
                    <Button
                      type="button"
                      variant="outline"
                      onClick={() => copyUrl('path', selectedPathPublicUrl)}
                      data-testid="path-visibility-copy-url"
                    >
                      {copiedTarget === 'path' ? 'Copied' : 'Copy public URL'}
                    </Button>
                    <span className="slice-settings-note">
                      Share this URL when the selected target resolves public.
                    </span>
                  </div>
                </div>
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      {copyError && <div className="panel-error">{copyError}</div>}

      <Card className="border-border/70">
        <CardContent className="pt-6">
          <div className="slice-settings-card-header">
            <div>
              <h4>Environment</h4>
              <p>Configure the default runtime environment for this slice.</p>
            </div>
          </div>

          {loading && <div className="panel-empty">Loading environment settings…</div>}
          {!loading && error && <div className="panel-error">{error}</div>}
          {!loading && success && <div className="panel-success">{success}</div>}

          {!loading && !error && (
            <div className="slice-settings-form">
              <label htmlFor="slice-settings-environment">Environment</label>
              <select
                id="slice-settings-environment"
                value={selectedEnvironment}
                onChange={(event) => {
                  setSelectedEnvironment(event.target.value);
                  setSuccess('');
                }}
                data-testid="slice-settings-environment-select"
              >
                <option value="" disabled>
                  {sortedEnvironments.length === 0 ? 'No environments available' : 'Select an environment'}
                </option>
                {sortedEnvironments.map((env) => (
                  <option key={env.name} value={env.name}>
                    {env.displayName || env.name}
                  </option>
                ))}
              </select>

              <div className="slice-settings-actions">
                <Button
                  type="button"
                  variant="secondary"
                  className="history-toggle"
                  disabled={!canSave}
                  onClick={saveEnvironment}
                  data-testid="slice-settings-save"
                >
                  {saving ? 'Saving…' : 'Save'}
                </Button>
                {!currentEnvironment && <span className="slice-settings-note">No default environment is set yet.</span>}
              </div>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
