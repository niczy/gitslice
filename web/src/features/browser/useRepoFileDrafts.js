import { useCallback, useState } from 'react';

export function useRepoFileDrafts({
  fileContent,
  initialDraftContent = '',
  selectedFile,
  setEncodedFileContent,
  setFileContent,
  setPreviewEncodedFileContent,
  setPreviewFileContent,
  setPreviewFilePath,
  setSelectedFileSize,
  setTreeEntries,
}) {
  const [draftContent, setDraftContent] = useState(initialDraftContent);
  const [fileDrafts, setFileDrafts] = useState({});
  const [isEditingFile, setIsEditingFile] = useState(false);

  const resetDraftState = useCallback((nextDraftContent = '') => {
    setDraftContent(nextDraftContent);
    setIsEditingFile(false);
  }, []);

  const resetAllDrafts = useCallback(() => {
    setFileDrafts({});
    resetDraftState('');
  }, [resetDraftState]);

  const showFileEditor = useCallback(() => {
    setDraftContent(fileContent);
    setIsEditingFile(true);
  }, [fileContent]);

  const cancelFileEdit = useCallback(() => {
    setDraftContent(fileContent);
    setIsEditingFile(false);
  }, [fileContent]);

  const confirmFileEdit = useCallback(() => {
    if (!selectedFile) {
      return;
    }
    setFileDrafts((prev) => ({ ...prev, [selectedFile]: draftContent }));
    setFileContent(draftContent);
    setEncodedFileContent('');
    setPreviewFilePath(selectedFile);
    setPreviewFileContent(draftContent);
    setPreviewEncodedFileContent('');
    setSelectedFileSize(draftContent.length);
    setIsEditingFile(false);
    const parentPath = selectedFile.includes('/') ? selectedFile.split('/').slice(0, -1).join('/') : '';
    setTreeEntries((prev) => {
      const entries = prev[parentPath] || [];
      const nextEntries = entries.map((entry) => {
        if (entry.path !== selectedFile) {
          return entry;
        }
        return { ...entry, size: draftContent.length };
      });
      return { ...prev, [parentPath]: nextEntries };
    });
  }, [
    draftContent,
    selectedFile,
    setEncodedFileContent,
    setFileContent,
    setPreviewEncodedFileContent,
    setPreviewFileContent,
    setPreviewFilePath,
    setSelectedFileSize,
    setTreeEntries,
  ]);

  return {
    cancelFileEdit,
    confirmFileEdit,
    draftContent,
    fileDrafts,
    isEditingFile,
    resetAllDrafts,
    resetDraftState,
    setDraftContent,
    showFileEditor,
  };
}
