import { useEffect, useMemo, useState } from 'react';
import {
  getSliceVisibility,
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

function visibilityTone(value) {
  return value === 'public' ? 'public' : 'private';
}

function visibilityLabel(value) {
  return value === 'public' ? 'Public' : 'Private';
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

export default function SliceSettings({ sliceId, sliceName }) {
  const [sliceVisibility, setSliceVisibility] = useState('private');
  const [slicePropagationMode, setSlicePropagationMode] = useState('unchanged');
  const [sliceVisibilityLoading, setSliceVisibilityLoading] = useState(true);
  const [sliceVisibilitySaving, setSliceVisibilitySaving] = useState(false);
  const [sliceVisibilityError, setSliceVisibilityError] = useState('');
  const [sliceVisibilitySuccess, setSliceVisibilitySuccess] = useState('');

  const [copiedTarget, setCopiedTarget] = useState('');
  const [copyError, setCopyError] = useState('');

  const slicePublicUrl = useMemo(() => buildPublicUrl(sliceId, '', 'directory'), [sliceId]);

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
    if (!copiedTarget && !copyError) {
      return undefined;
    }
    const timer = window.setTimeout(() => {
      setCopiedTarget('');
      setCopyError('');
    }, 2200);
    return () => window.clearTimeout(timer);
  }, [copiedTarget, copyError]);

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
      setSliceVisibilitySuccess(`Slice visibility is now ${nextVisibility}.`);
    } catch (err) {
      setSliceVisibilityError(err?.message || 'Unable to update slice visibility.');
    } finally {
      setSliceVisibilitySaving(false);
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

  return (
    <div className="slice-settings" data-testid="slice-settings-panel">
      <div className="slice-settings-header">
        <h3>Slice settings</h3>
        <p>
          Manage visibility and public links for <strong>{sliceName || sliceId}</strong>.
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
      </div>

      {copyError && <div className="panel-error">{copyError}</div>}
    </div>
  );
}
