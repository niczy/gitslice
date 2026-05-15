import { useEffect, useState } from 'react';
import {
  getSliceVisibility,
  updateSliceVisibility,
  addSliceFolder,
  removeSliceFolder,
} from '../utils/api.js';
import { SliceVisibilityCard } from './settings/SliceVisibilityCard.jsx';
import { SliceEnvKVCard } from './settings/SliceEnvKVCard.jsx';
import { TrackedFoldersCard } from './settings/TrackedFoldersCard.jsx';
import {
  normalizePathPropagationMode,
  normalizeVisibility,
  pathPropagationRequestValue,
  visibilityRequestValue,
} from './settings/SliceSettingsHelpers.js';

function readSettingField(value, camelName, snakeName, fallback = '') {
  return value?.[camelName] ?? value?.[snakeName] ?? fallback;
}

function getInitialVisibilityPayload(initialSettingsData, sliceId) {
  const initialSliceId = readSettingField(initialSettingsData, 'sliceId', 'slice_id', '');
  if (!sliceId || initialSliceId !== sliceId) {
    return null;
  }
  return initialSettingsData?.visibility || null;
}

function getInitialVisibilityError(initialSettingsData, sliceId) {
  const initialSliceId = readSettingField(initialSettingsData, 'sliceId', 'slice_id', '');
  if (!sliceId || initialSliceId !== sliceId) {
    return '';
  }
  return readSettingField(initialSettingsData, 'visibilityError', 'visibility_error', '');
}

export default function SliceSettings({
  sliceId,
  sliceName,
  folderMounts,
  onFolderMountsChange,
  initialSettingsData = null,
}) {
  const initialVisibilityPayload = getInitialVisibilityPayload(initialSettingsData, sliceId);
  const initialVisibilityError = getInitialVisibilityError(initialSettingsData, sliceId);
  const [sliceVisibility, setSliceVisibility] = useState(() => (
    normalizeVisibility(readSettingField(initialVisibilityPayload, 'visibility', 'visibility', 'private'))
  ));
  const [slicePropagationMode, setSlicePropagationMode] = useState(() => (
    normalizePathPropagationMode(
      readSettingField(initialVisibilityPayload, 'pathPropagationMode', 'path_propagation_mode', 'unchanged'),
    )
  ));
  const [loadedVisibilitySliceId, setLoadedVisibilitySliceId] = useState(() => (
    initialVisibilityPayload || initialVisibilityError ? sliceId : ''
  ));
  const [sliceVisibilityLoading, setSliceVisibilityLoading] = useState(() => (
    Boolean(sliceId) && !initialVisibilityPayload && !initialVisibilityError
  ));
  const [sliceVisibilitySaving, setSliceVisibilitySaving] = useState(false);
  const [sliceVisibilityError, setSliceVisibilityError] = useState(() => initialVisibilityError);
  const [sliceVisibilitySuccess, setSliceVisibilitySuccess] = useState('');

  const [localMounts, setLocalMounts] = useState(folderMounts || []);
  const [newFolderPath, setNewFolderPath] = useState('');
  const [folderAdding, setFolderAdding] = useState(false);
  const [folderRemoving, setFolderRemoving] = useState('');
  const [folderError, setFolderError] = useState('');

  useEffect(() => {
    setLocalMounts(folderMounts || []);
  }, [folderMounts]);

  useEffect(() => {
    const nextVisibilityPayload = getInitialVisibilityPayload(initialSettingsData, sliceId);
    const nextVisibilityError = getInitialVisibilityError(initialSettingsData, sliceId);
    if (!nextVisibilityPayload && !nextVisibilityError) {
      return;
    }
    if (nextVisibilityPayload) {
      setSliceVisibility(normalizeVisibility(readSettingField(nextVisibilityPayload, 'visibility', 'visibility', 'private')));
      setSlicePropagationMode(normalizePathPropagationMode(
        readSettingField(nextVisibilityPayload, 'pathPropagationMode', 'path_propagation_mode', 'unchanged'),
      ));
    }
    setSliceVisibilityError(nextVisibilityError || '');
    setSliceVisibilitySuccess('');
    setSliceVisibilityLoading(false);
    setLoadedVisibilitySliceId(sliceId);
  }, [initialSettingsData, sliceId]);

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
      setLoadedVisibilitySliceId('');
      return;
    }
    if (loadedVisibilitySliceId === sliceId) {
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
        setSlicePropagationMode(normalizePathPropagationMode(response?.path_propagation_mode ?? response?.pathPropagationMode));
      } catch (err) {
        if (!active) {
          return;
        }
        setSliceVisibilityError(err?.message || 'Unable to load slice visibility.');
      } finally {
        if (active) {
          setSliceVisibilityLoading(false);
          setLoadedVisibilitySliceId(sliceId);
        }
      }
    };

    load();
    return () => {
      active = false;
    };
  }, [loadedVisibilitySliceId, sliceId]);

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
          Manage visibility, tracked folders, and environment values for <strong>{sliceName || sliceId}</strong>.
        </p>
      </div>

      <div className="slice-settings-grid">
        <SliceVisibilityCard
          onPropagationModeChange={setSlicePropagationMode}
          onSaveVisibility={saveSliceVisibility}
          slicePropagationMode={slicePropagationMode}
          sliceVisibility={sliceVisibility}
          sliceVisibilityError={sliceVisibilityError}
          sliceVisibilityLoading={sliceVisibilityLoading}
          sliceVisibilitySaving={sliceVisibilitySaving}
          sliceVisibilitySuccess={sliceVisibilitySuccess}
        />

        <TrackedFoldersCard
          folderAdding={folderAdding}
          folderError={folderError}
          folderRemoving={folderRemoving}
          localMounts={localMounts}
          newFolderPath={newFolderPath}
          onAddFolder={addFolder}
          onFolderKeyDown={handleFolderKeyDown}
          onNewFolderPathChange={setNewFolderPath}
          onRemoveFolder={removeFolder}
        />

        <SliceEnvKVCard sliceId={sliceId} initialEnvData={initialSettingsData?.env || null} />
      </div>
    </div>
  );
}
