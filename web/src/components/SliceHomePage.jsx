import { useEffect, useMemo, useState } from 'react';

import { createSliceFromFolder, fetchSliceEntries } from '../utils/api.js';
import { SliceCreateDialog } from './slices/SliceCreateDialog.jsx';
import { SliceHomeHeader } from './slices/SliceHomeHeader.jsx';
import { SliceHomeList } from './slices/SliceHomeList.jsx';
import {
  cleanFolderPath,
  getHomeRootPath,
  pathRelativeToHomeRoot,
  pathUnderHomeRoot,
  sortSlices,
  validateFolderPath,
} from './slices/SliceHomeHelpers.js';

export default function SliceHomePage({
  slices,
  slicesLoading,
  slicesError,
  isAuthenticated,
  username,
  homeSliceId,
  onOpenSlice,
  onRefresh,
  onRequireLogin,
}) {
  const [query, setQuery] = useState('');
  const [isCreateOpen, setIsCreateOpen] = useState(false);
  const [sliceName, setSliceName] = useState('');
  const [sliceDescription, setSliceDescription] = useState('');
  const [selectedFolders, setSelectedFolders] = useState([]);
  const [folderInput, setFolderInput] = useState('');
  const [folderSelectionError, setFolderSelectionError] = useState('');
  const [folderBrowserEntries, setFolderBrowserEntries] = useState({});
  const [expandedFolderPaths, setExpandedFolderPaths] = useState(['']);
  const [loadingFolderPaths, setLoadingFolderPaths] = useState({});
  const [createError, setCreateError] = useState('');
  const [createLoading, setCreateLoading] = useState(false);
  const homeRootPath = getHomeRootPath(username, homeSliceId);
  const createParentSliceId = homeSliceId || (homeRootPath ? `home_${homeRootPath}` : '');

  const filteredSlices = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase();
    const sorted = sortSlices(slices || [], homeSliceId);
    if (!normalizedQuery) {
      return sorted;
    }
    return sorted.filter((slice) => {
      const fields = [
        slice?.slice_id,
        slice?.slug,
        slice?.name,
        slice?.description,
      ].map((value) => String(value || '').toLowerCase());
      return fields.some((value) => value.includes(normalizedQuery));
    });
  }, [homeSliceId, query, slices]);

  const openCreateDialog = () => {
    if (!isAuthenticated) {
      onRequireLogin?.();
      return;
    }
    setCreateError('');
    setFolderSelectionError('');
    setFolderBrowserEntries({});
    setExpandedFolderPaths(['']);
    setLoadingFolderPaths({});
    setIsCreateOpen(true);
  };

  const closeCreateDialog = () => {
    setIsCreateOpen(false);
  };

  const loadFolderEntries = async (path = '') => {
    if (!createParentSliceId || !homeRootPath) {
      setFolderSelectionError('Your home folder is still loading.');
      return;
    }
    if (Object.prototype.hasOwnProperty.call(folderBrowserEntries, path)) {
      return;
    }
    setLoadingFolderPaths((prev) => ({ ...prev, [path]: true }));
    setFolderSelectionError('');
    try {
      const entries = await fetchSliceEntries(createParentSliceId, pathUnderHomeRoot(homeRootPath, path));
      setFolderBrowserEntries((prev) => ({ ...prev, [path]: entries }));
    } catch (error) {
      setFolderSelectionError(error?.message || 'Unable to load folders.');
    } finally {
      setLoadingFolderPaths((prev) => ({ ...prev, [path]: false }));
    }
  };

  useEffect(() => {
    if (isCreateOpen) {
      loadFolderEntries('');
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isCreateOpen]);

  const addFolderSelection = (rawPath) => {
    const { path: validatedPath, error } = validateFolderPath(rawPath);
    if (error) {
      setFolderSelectionError(error);
      return;
    }
    const relativeHomePath = pathRelativeToHomeRoot(homeRootPath, validatedPath);
    const path = relativeHomePath || validatedPath;
    if (cleanFolderPath(path) === cleanFolderPath(homeRootPath)) {
      setFolderSelectionError('Choose a folder inside your home directory.');
      return;
    }

    if (selectedFolders.includes(path)) {
      setFolderSelectionError('That folder is already tracked.');
      return;
    }
    if (selectedFolders.some((folder) => path.startsWith(`${folder}/`))) {
      setFolderSelectionError('A parent folder is already tracked.');
      return;
    }

    setSelectedFolders((prev) => [
      ...prev.filter((folder) => !folder.startsWith(`${path}/`)),
      path,
    ]);
    setFolderInput('');
    setFolderSelectionError('');
    setCreateError('');
  };

  const removeFolderSelection = (folderPath) => {
    setSelectedFolders((prev) => prev.filter((folder) => folder !== folderPath));
  };

  const toggleFolderExpansion = async (folderPath) => {
    const isExpanded = expandedFolderPaths.includes(folderPath);
    if (isExpanded) {
      setExpandedFolderPaths((prev) => prev.filter((path) => path !== folderPath));
      return;
    }
    await loadFolderEntries(folderPath);
    setExpandedFolderPaths((prev) => [...prev, folderPath]);
  };

  const handleFolderInputSubmit = () => {
    addFolderSelection(folderInput);
  };

  const handleCreateSubmit = async (event) => {
    event.preventDefault();
    const name = sliceName.trim();
    if (!name) {
      setCreateError('Slice name is required.');
      return;
    }
    if (selectedFolders.length === 0) {
      setCreateError('At least one tracked folder is required.');
      return;
    }

    setCreateLoading(true);
    setCreateError('');
    try {
      const created = await createSliceFromFolder({
        parentSliceId: createParentSliceId,
        folderPaths: selectedFolders,
        name,
        description: sliceDescription.trim(),
      });
      setSliceName('');
      setSliceDescription('');
      setSelectedFolders([]);
      setFolderInput('');
      setIsCreateOpen(false);
      await onRefresh?.();
      onOpenSlice(created.slice_id);
    } catch (error) {
      setCreateError(error?.message || 'Unable to create slice.');
    } finally {
      setCreateLoading(false);
    }
  };

  return (
    <section className="slice-home" data-testid="slice-home-page">
      <SliceHomeHeader onCreate={openCreateDialog} />

      <SliceHomeList
        filteredSlices={filteredSlices}
        homeSliceId={homeSliceId}
        onOpenSlice={onOpenSlice}
        query={query}
        setQuery={setQuery}
        slicesError={slicesError}
        slicesLoading={slicesLoading}
      />

      {isCreateOpen && (
        <SliceCreateDialog
          createError={createError}
          createLoading={createLoading}
          expandedFolderPaths={expandedFolderPaths}
          folderBrowserEntries={folderBrowserEntries}
          folderInput={folderInput}
          folderSelectionError={folderSelectionError}
          homeRootPath={homeRootPath}
          loadingFolderPaths={loadingFolderPaths}
          onAddFolder={addFolderSelection}
          onClose={closeCreateDialog}
          onDescriptionChange={setSliceDescription}
          onFolderInputChange={setFolderInput}
          onFolderInputSubmit={handleFolderInputSubmit}
          onNameChange={setSliceName}
          onRemoveFolder={removeFolderSelection}
          onSubmit={handleCreateSubmit}
          onToggleFolderExpansion={toggleFolderExpansion}
          selectedFolders={selectedFolders}
          sliceDescription={sliceDescription}
          sliceName={sliceName}
        />
      )}
    </section>
  );
}
