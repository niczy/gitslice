import { useEffect, useState } from 'react';
import {
  getSliceVisibility,
  updateSliceVisibility,
  addSliceFolder,
  removeSliceFolder,
} from '../utils/api.js';
import { Badge } from './ui/badge.jsx';
import { Button } from './ui/button.jsx';
import { Card, CardContent } from './ui/card.jsx';
import { Plus, Trash2, Folder } from 'lucide-react';

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

export default function SliceSettings({ sliceId, sliceName, folderMounts, onFolderMountsChange }) {
  const [sliceVisibility, setSliceVisibility] = useState('private');
  const [slicePropagationMode, setSlicePropagationMode] = useState('unchanged');
  const [sliceVisibilityLoading, setSliceVisibilityLoading] = useState(true);
  const [sliceVisibilitySaving, setSliceVisibilitySaving] = useState(false);
  const [sliceVisibilityError, setSliceVisibilityError] = useState('');
  const [sliceVisibilitySuccess, setSliceVisibilitySuccess] = useState('');

  const [localMounts, setLocalMounts] = useState(folderMounts || []);
  const [newFolderPath, setNewFolderPath] = useState('');
  const [folderAdding, setFolderAdding] = useState(false);
  const [folderRemoving, setFolderRemoving] = useState('');
  const [folderError, setFolderError] = useState('');

  useEffect(() => {
    setLocalMounts(folderMounts || []);
  }, [folderMounts]);

  const addFolder = async () => {
    const path = (newFolderPath || '').trim();
    if (!path || !sliceId || folderAdding) {
      return;
    }
    setFolderAdding(true);
    setFolderError('');
    try {
      const response = await addSliceFolder(sliceId, path);
      const updatedMounts = response?.folder_mounts || response?.folderMounts || [];
      setLocalMounts(updatedMounts);
      if (onFolderMountsChange) {
        onFolderMountsChange(updatedMounts);
      }
      setNewFolderPath('');
    } catch (err) {
      setFolderError(err?.message || 'Unable to add tracked folder.');
    } finally {
      setFolderAdding(false);
    }
  };

  const removeFolder = async (folderPath) => {
    if (!sliceId || folderRemoving) {
      return;
    }
    setFolderRemoving(folderPath);
    setFolderError('');
    try {
      const response = await removeSliceFolder(sliceId, folderPath);
      const updatedMounts = response?.folder_mounts || response?.folderMounts || [];
      setLocalMounts(updatedMounts);
      if (onFolderMountsChange) {
        onFolderMountsChange(updatedMounts);
      }
    } catch (err) {
      setFolderError(err?.message || 'Unable to remove tracked folder.');
    } finally {
      setFolderRemoving('');
    }
  };

  const handleFolderKeyDown = (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      addFolder();
    }
  };

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

  return (
    <div className="slice-settings" data-testid="slice-settings-panel">
      <div className="slice-settings-header">
        <h3>Slice settings</h3>
        <p>
          Manage visibility for <strong>{sliceName || sliceId}</strong>.
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
              </div>
            )}
          </CardContent>
        </Card>

        <Card className="border-border/70">
          <CardContent className="pt-6">
            <div className="slice-settings-card-header">
              <div>
                <h4>Tracked folders</h4>
                <p>Folders from the parent slice that this custom slice tracks.</p>
              </div>
            </div>

            {folderError && <div className="panel-error">{folderError}</div>}

            {localMounts.length === 0 && (
              <div className="panel-empty">No tracked folders configured.</div>
            )}

            {localMounts.length > 0 && (
              <div className="tracked-folders-list">
                {localMounts.map((mount) => {
                  const mountSource = mount?.source_path || mount?.sourcePath || '';
                  const mountAlias = mount?.alias || '';
                  return (
                    <div key={mountSource} className="tracked-folder-row" data-testid="tracked-folder-row">
                      <div className="tracked-folder-info">
                        <Folder size={14} className="tracked-folder-icon" />
                        <span className="tracked-folder-source">{mountSource}</span>
                        {mountAlias && mountAlias !== mountSource.split('/').pop() && (
                          <span className="tracked-folder-alias">→ {mountAlias}</span>
                        )}
                      </div>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon"
                        disabled={folderRemoving === mountSource}
                        onClick={() => removeFolder(mountSource)}
                        data-testid="remove-tracked-folder"
                        title={`Remove ${mountSource}`}
                      >
                        <Trash2 size={14} aria-hidden="true" />
                      </Button>
                    </div>
                  );
                })}
              </div>
            )}

            <div className="tracked-folder-add">
              <div className="tracked-folder-input-group">
                <input
                  type="text"
                  className="tracked-folder-input"
                  placeholder="src/components"
                  value={newFolderPath}
                  onChange={(e) => setNewFolderPath(e.target.value)}
                  onKeyDown={handleFolderKeyDown}
                  data-testid="tracked-folder-input"
                />
                <Button
                  type="button"
                  size="sm"
                  disabled={folderAdding || !newFolderPath.trim()}
                  onClick={addFolder}
                  data-testid="add-tracked-folder"
                >
                  <Plus size={14} aria-hidden="true" />
                  {folderAdding ? 'Adding…' : 'Add folder'}
                </Button>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
