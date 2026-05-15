import { useCallback, useState } from 'react';

import {
  createChangeset,
  fetchWithAuth,
  mergeChangeset,
} from '../../utils/api.js';
import { normalizeEntryType } from '../../utils/normalize.js';
import {
  buildBrowserEntriesUrl,
  normalizeWorkspaceResultPath,
  readBrowserErrorMessage,
} from './browserApi.js';
import {
  getDirectoryAncestorPaths,
  getEntryName,
} from './browserModel.js';
import {
  assertCanCreateUnderTreePath,
  assertTreeMutationPathsAllowed,
  encodeTreeTextContent,
  entryLabelForPrompt,
  getDefaultTreeActionTarget,
  getTreeCreateBlockedReason,
  getDirectoryMarkerPath,
  getTreeEntryBaseName,
  getTreeEntryParentPath,
  isSuccessfulMergeResponse,
  joinTreePath,
  mergeResponseErrorMessage,
  normalizeChangesetId,
  normalizeTreeOperationName,
  pathExistsInEntries,
  remapChildPathForRename,
} from './browserTreeOperations.js';

const EMPTY_ACTION_STATE = {
  busy: false,
  error: '',
  message: '',
  targetPath: '',
};

function promptForTreeName(message, defaultValue = '') {
  if (typeof window === 'undefined' || typeof window.prompt !== 'function') {
    return null;
  }
  const value = window.prompt(message, defaultValue);
  if (value === null) {
    return null;
  }
  return normalizeTreeOperationName(value);
}

function confirmTreeAction(message) {
  if (typeof window === 'undefined' || typeof window.confirm !== 'function') {
    return true;
  }
  return window.confirm(message);
}

function actionMessageName(path) {
  return getTreeEntryBaseName(path) || 'root';
}

function uniquePaths(paths) {
  return Array.from(new Set(paths.map((path) => normalizeWorkspaceResultPath(path)).filter(Boolean)));
}

export function useRepoBrowserTreeActions({
  apiBaseUrl,
  buildEntriesUrl,
  buildFileUrl,
  canLoad,
  clearFilePreview,
  currentSlice,
  focusedEntry,
  openFilesView,
  setError,
  setExpandedPaths,
  setFileError,
  setFocusedEntry,
  setIsLoading,
  setSliceHash,
  setTreeEntries,
  sliceHash,
  sliceId,
  treeEntriesScopeRef,
  writeBrowserState,
}) {
  const [treeActionState, setTreeActionState] = useState(EMPTY_ACTION_STATE);

  const getCreateParentPath = useCallback((entry) => {
    const target = entry
      ? { path: normalizeWorkspaceResultPath(entry.path), type: normalizeEntryType(entry.type) }
      : getDefaultTreeActionTarget(focusedEntry);
    return target.type === 'directory' ? target.path : getTreeEntryParentPath(target.path);
  }, [focusedEntry]);

  const getCreateTreeEntryBlockedReason = useCallback((entry = null) => {
    return getTreeCreateBlockedReason({
      sliceId,
      currentSlice,
      parentPath: getCreateParentPath(entry),
    });
  }, [currentSlice, getCreateParentPath, sliceId]);

  const fetchEntriesForAction = useCallback(async (path, options = {}) => {
    const normalizedPath = normalizeWorkspaceResultPath(path);
    const url = options.head
      ? buildBrowserEntriesUrl({
        apiBaseUrl,
        sliceId,
        path: normalizedPath,
        sliceHash: '',
      })
      : buildEntriesUrl(normalizedPath);
    const response = await fetchWithAuth(url);
    if (!response.ok) {
      throw new Error(await readBrowserErrorMessage(response, 'Unable to load folder entries'));
    }
    const payload = await response.json();
    return payload?.entries || [];
  }, [apiBaseUrl, buildEntriesUrl, sliceId]);

  const fetchFileForAction = useCallback(async (path) => {
    const response = await fetchWithAuth(buildFileUrl(path));
    if (!response.ok) {
      throw new Error(await readBrowserErrorMessage(response, 'Unable to load file content'));
    }
    const payload = await response.json();
    return payload?.file || {};
  }, [buildFileUrl]);

  const collectDirectoryFiles = useCallback(async (directoryPath) => {
    const collect = async (path) => {
      const entries = await fetchEntriesForAction(path);
      const files = [];
      for (const entry of entries) {
        const entryKind = normalizeEntryType(entry?.type);
        const entryPath = normalizeWorkspaceResultPath(entry?.path || '');
        if (!entryPath) {
          continue;
        }
        if (entryKind === 'directory') {
          files.push(...await collect(entryPath));
        } else {
          files.push(entryPath);
        }
      }
      return files;
    };
    return collect(directoryPath);
  }, [fetchEntriesForAction]);

  const refreshTreeAfterMerge = useCallback(async ({
    clearSelection = false,
    directoryPath = '',
  } = {}) => {
    const normalizedDirectory = normalizeWorkspaceResultPath(directoryPath);
    const expandedAncestors = getDirectoryAncestorPaths(normalizedDirectory);

    treeEntriesScopeRef.current = '';
    setExpandedPaths(expandedAncestors);

    if (clearSelection) {
      clearFilePreview();
      openFilesView();
      setFocusedEntry({ path: normalizedDirectory, type: 'directory' });
      writeBrowserState({ dir: normalizedDirectory }, { replace: true });
    }

    if (sliceHash) {
      setSliceHash('');
      setTreeEntries({});
      return;
    }

    const loadedEntries = {};
    for (const path of expandedAncestors) {
      loadedEntries[path] = await fetchEntriesForAction(path, { head: true });
    }
    setTreeEntries(loadedEntries);
  }, [
    clearFilePreview,
    fetchEntriesForAction,
    openFilesView,
    setExpandedPaths,
    setFocusedEntry,
    setSliceHash,
    setTreeEntries,
    sliceHash,
    treeEntriesScopeRef,
    writeBrowserState,
  ]);

  const createAndMergeChangeset = useCallback(async ({ fileContents, message, modifiedFiles }) => {
    assertTreeMutationPathsAllowed({
      sliceId,
      currentSlice,
      paths: modifiedFiles,
    });
    const createResponse = await createChangeset({
      sliceId,
      baseCommitHash: sliceHash,
      modifiedFiles: uniquePaths(modifiedFiles),
      message,
      fileContents,
    });
    const changesetId = normalizeChangesetId(createResponse);
    if (!changesetId) {
      throw new Error('Changeset was created without an id.');
    }
    const mergeResponse = await mergeChangeset(changesetId);
    if (!isSuccessfulMergeResponse(mergeResponse)) {
      throw new Error(mergeResponseErrorMessage(mergeResponse));
    }
    return mergeResponse;
  }, [currentSlice, sliceHash, sliceId]);

  const createFile = useCallback(async (entry) => {
    const parentPath = getCreateParentPath(entry);
    assertCanCreateUnderTreePath({ sliceId, currentSlice, parentPath });
    const name = promptForTreeName('New file name');
    if (name === null) {
      return null;
    }
    const filePath = joinTreePath(parentPath, name);
    const existingEntries = await fetchEntriesForAction(parentPath);
    if (pathExistsInEntries(existingEntries, filePath)) {
      throw new Error(`"${name}" already exists in this folder.`);
    }
    await createAndMergeChangeset({
      message: `Create ${filePath}`,
      modifiedFiles: [filePath],
      fileContents: [{ path: filePath, content: '' }],
    });
    await refreshTreeAfterMerge({ directoryPath: parentPath });
    return `Created ${actionMessageName(filePath)}.`;
  }, [createAndMergeChangeset, currentSlice, fetchEntriesForAction, getCreateParentPath, refreshTreeAfterMerge, sliceId]);

  const createFolder = useCallback(async (entry) => {
    const parentPath = getCreateParentPath(entry);
    assertCanCreateUnderTreePath({ sliceId, currentSlice, parentPath });
    const name = promptForTreeName('New folder name');
    if (name === null) {
      return null;
    }
    const folderPath = joinTreePath(parentPath, name);
    const existingEntries = await fetchEntriesForAction(parentPath);
    if (pathExistsInEntries(existingEntries, folderPath)) {
      throw new Error(`"${name}" already exists in this folder.`);
    }
    const markerPath = getDirectoryMarkerPath(folderPath);
    await createAndMergeChangeset({
      message: `Create ${folderPath}`,
      modifiedFiles: [markerPath],
      fileContents: [{
        path: markerPath,
        content: encodeTreeTextContent(''),
      }],
    });
    await refreshTreeAfterMerge({ directoryPath: folderPath, clearSelection: true });
    return `Created ${actionMessageName(folderPath)}.`;
  }, [createAndMergeChangeset, currentSlice, fetchEntriesForAction, getCreateParentPath, refreshTreeAfterMerge, sliceId]);

  const deleteEntry = useCallback(async (entry) => {
    const entryKind = normalizeEntryType(entry?.type);
    const entryPath = normalizeWorkspaceResultPath(entry?.path || '');
    if (!entryPath) {
      throw new Error('The slice root cannot be deleted from the file tree.');
    }
    const label = entryLabelForPrompt(entry);
    if (!confirmTreeAction(`Delete ${label}? This creates and merges a changeset.`)) {
      return null;
    }

    let deletedPaths = [entryPath];
    if (entryKind === 'directory') {
      deletedPaths = await collectDirectoryFiles(entryPath);
      if (deletedPaths.length === 0) {
        throw new Error('This folder has no committed files to delete.');
      }
    }

    await createAndMergeChangeset({
      message: `Delete ${entryPath}`,
      modifiedFiles: deletedPaths,
      fileContents: deletedPaths.map((path) => ({ path, deleted: true })),
    });
    await refreshTreeAfterMerge({
      directoryPath: getTreeEntryParentPath(entryPath),
      clearSelection: true,
    });
    return `Deleted ${getEntryName(entry)}.`;
  }, [collectDirectoryFiles, createAndMergeChangeset, refreshTreeAfterMerge]);

  const renameEntry = useCallback(async (entry) => {
    const entryKind = normalizeEntryType(entry?.type);
    const entryPath = normalizeWorkspaceResultPath(entry?.path || '');
    if (!entryPath) {
      throw new Error('The slice root cannot be renamed from the file tree.');
    }
    const oldName = getTreeEntryBaseName(entryPath);
    const newName = promptForTreeName(`Rename ${entryLabelForPrompt(entry)} to`, oldName);
    if (newName === null || newName === oldName) {
      return null;
    }
    const parentPath = getTreeEntryParentPath(entryPath);
    const newPath = joinTreePath(parentPath, newName);
    assertTreeMutationPathsAllowed({
      sliceId,
      currentSlice,
      paths: [newPath],
    });
    const existingEntries = await fetchEntriesForAction(parentPath);
    if (pathExistsInEntries(existingEntries, newPath)) {
      throw new Error(`"${newName}" already exists in this folder.`);
    }

    if (entryKind === 'directory') {
      const oldFilePaths = await collectDirectoryFiles(entryPath);
      if (oldFilePaths.length === 0) {
        throw new Error('This folder has no committed files to rename.');
      }
      const copiedContents = [];
      for (const oldFilePath of oldFilePaths) {
        const file = await fetchFileForAction(oldFilePath);
        copiedContents.push({
          path: remapChildPathForRename(entryPath, newPath, oldFilePath),
          content: file?.content || '',
        });
      }
      await createAndMergeChangeset({
        message: `Rename ${entryPath} to ${newPath}`,
        modifiedFiles: [
          ...oldFilePaths,
          ...copiedContents.map((change) => change.path),
        ],
        fileContents: [
          ...copiedContents,
          ...oldFilePaths.map((path) => ({ path, deleted: true })),
        ],
      });
      await refreshTreeAfterMerge({
        directoryPath: newPath,
        clearSelection: true,
      });
      return `Renamed ${oldName} to ${newName}.`;
    }

    const file = await fetchFileForAction(entryPath);
    await createAndMergeChangeset({
      message: `Rename ${entryPath} to ${newPath}`,
      modifiedFiles: [entryPath, newPath],
      fileContents: [
        { path: newPath, content: file?.content || '' },
        { path: entryPath, deleted: true },
      ],
    });
    await refreshTreeAfterMerge({
      directoryPath: parentPath,
      clearSelection: true,
    });
    return `Renamed ${oldName} to ${newName}.`;
  }, [
    collectDirectoryFiles,
    createAndMergeChangeset,
    currentSlice,
    fetchEntriesForAction,
    fetchFileForAction,
    refreshTreeAfterMerge,
    sliceId,
  ]);

  const handleTreeAction = useCallback(async (action, entry = null) => {
    if (!canLoad || !sliceId) {
      return;
    }
    if (treeActionState.busy) {
      return;
    }

    const targetPath = normalizeWorkspaceResultPath(entry?.path || focusedEntry?.path || '');
    setTreeActionState({
      ...EMPTY_ACTION_STATE,
      busy: true,
      targetPath,
    });
    setError('');
    setFileError('');
    setIsLoading(true);

    try {
      let message = null;
      if (action === 'create-file') {
        message = await createFile(entry);
      } else if (action === 'create-folder') {
        message = await createFolder(entry);
      } else if (action === 'delete') {
        message = await deleteEntry(entry);
      } else if (action === 'rename') {
        message = await renameEntry(entry);
      }
      setTreeActionState({
        ...EMPTY_ACTION_STATE,
        message: message || '',
      });
    } catch (error) {
      const errorMessage = error?.message || 'File tree action failed.';
      setTreeActionState({
        ...EMPTY_ACTION_STATE,
        error: errorMessage,
      });
      setError(errorMessage);
    } finally {
      setIsLoading(false);
    }
  }, [
    canLoad,
    createFile,
    createFolder,
    deleteEntry,
    focusedEntry,
    renameEntry,
    setError,
    setFileError,
    setIsLoading,
    sliceId,
    treeActionState.busy,
  ]);

  return {
    getCreateTreeEntryBlockedReason,
    handleTreeAction,
    treeActionState,
  };
}
