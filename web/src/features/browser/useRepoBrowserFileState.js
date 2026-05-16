import { useCallback, useMemo, useState } from 'react';

import { decodeBase64, highlightCodeLines } from '../../utils/highlight.js';
import { renderMarkdownHtml } from '../../utils/markdown.js';
import {
  getFilePayloadSize,
  getPreviewMeta,
} from './browserModel.js';
import { useRepoBrowserHistory } from './useRepoBrowserHistory.js';
import { useRepoFileDrafts } from './useRepoFileDrafts.js';

export function useRepoBrowserFileState({
  apiBaseUrl,
  buildRawFileUrl,
  hasInitialSelectedFilePayload,
  initialBrowserData,
  initialDataMatchesRawSlice,
  initialSelectedFilePath,
  initialSelectedFilePayload,
  isActive,
  refreshHistoryToken,
  setTreeEntries,
  sliceId,
}) {
  const initialEncodedContent = initialSelectedFilePayload?.content || '';
  const getInitialDecodedContent = () => (
    initialEncodedContent ? decodeBase64(initialEncodedContent) : ''
  );
  const getInitialPathBase = () => (
    initialSelectedFilePayload?.pathBase || initialSelectedFilePayload?.path_base || null
  );

  const [selectedFile, setSelectedFile] = useState(() => initialSelectedFilePath || null);
  const [fileContent, setFileContent] = useState(getInitialDecodedContent);
  const [encodedFileContent, setEncodedFileContent] = useState(() => initialEncodedContent);
  const [previewFilePath, setPreviewFilePath] = useState(() => (
    initialEncodedContent ? initialSelectedFilePath : ''
  ));
  const [previewFileContent, setPreviewFileContent] = useState(getInitialDecodedContent);
  const [previewEncodedFileContent, setPreviewEncodedFileContent] = useState(() => initialEncodedContent);
  const [selectedFileSize, setSelectedFileSize] = useState(() => (
    getFilePayloadSize(initialSelectedFilePayload, getInitialDecodedContent())
  ));
  const [selectedFilePathBase, setSelectedFilePathBase] = useState(getInitialPathBase);
  const [loadingFilePath, setLoadingFilePath] = useState(() => (
    initialSelectedFilePath && !hasInitialSelectedFilePayload ? initialSelectedFilePath : ''
  ));
  const [fileError, setFileError] = useState(() => (
    initialDataMatchesRawSlice ? initialBrowserData?.selectedFileError || '' : ''
  ));

  const {
    cancelFileEdit,
    confirmFileEdit,
    draftContent,
    fileDrafts,
    isEditingFile,
    resetAllDrafts,
    resetDraftState,
    setDraftContent,
    showFileEditor,
  } = useRepoFileDrafts({
    fileContent,
    initialDraftContent: getInitialDecodedContent(),
    selectedFile,
    setEncodedFileContent,
    setFileContent,
    setPreviewEncodedFileContent,
    setPreviewFileContent,
    setPreviewFilePath,
    setSelectedFileSize,
    setTreeEntries,
  });

  const {
    fileHistory,
    historyError,
    historyLoading,
    resetHistory,
    showHistory,
    toggleHistory,
  } = useRepoBrowserHistory({
    apiBaseUrl,
    isActive,
    refreshHistoryToken,
    selectedFile,
    sliceId,
  });

  const highlightedContent = useMemo(() => (
    highlightCodeLines(previewFileContent).html
  ), [previewFileContent]);
  const markdownContent = useMemo(() => renderMarkdownHtml(previewFileContent), [previewFileContent]);
  const previewPath = previewFilePath || selectedFile || '';
  const previewMeta = useMemo(() => (
    getPreviewMeta(previewPath, previewEncodedFileContent)
  ), [previewEncodedFileContent, previewPath]);
  const hasPreviewContent = Boolean(previewFilePath);
  const isSelectedFileLoading = Boolean(selectedFile && loadingFilePath === selectedFile && !showHistory);
  const displayedFileSize = useMemo(() => {
    if (!selectedFile) {
      return null;
    }
    if (selectedFileSize !== null) {
      return selectedFileSize;
    }
    return isSelectedFileLoading ? null : fileContent.length;
  }, [fileContent.length, isSelectedFileLoading, selectedFile, selectedFileSize]);

  const clearFilePreview = useCallback(() => {
    setSelectedFile(null);
    setFileContent('');
    setEncodedFileContent('');
    setPreviewFilePath('');
    setPreviewFileContent('');
    setPreviewEncodedFileContent('');
    setSelectedFileSize(null);
    setSelectedFilePathBase(null);
    resetDraftState('');
    setFileError('');
    setLoadingFilePath('');
    resetHistory();
  }, [resetDraftState, resetHistory]);

  const openRawFile = useCallback(() => {
    if (!selectedFile || typeof window === 'undefined') {
      return;
    }
    window.open(buildRawFileUrl(selectedFile), '_blank', 'noopener,noreferrer');
  }, [buildRawFileUrl, selectedFile]);

  return {
    cancelFileEdit,
    clearFilePreview,
    confirmFileEdit,
    displayedFileSize,
    draftContent,
    encodedFileContent,
    fileContent,
    fileDrafts,
    fileError,
    fileHistory,
    hasPreviewContent,
    highlightedContent,
    historyError,
    historyLoading,
    isEditingFile,
    isSelectedFileLoading,
    loadingFilePath,
    markdownContent,
    openRawFile,
    previewEncodedFileContent,
    previewFileContent,
    previewFilePath,
    previewMeta,
    previewPath,
    resetAllDrafts,
    resetDraftState,
    resetHistory,
    selectedFile,
    selectedFilePathBase,
    selectedFileSize,
    setDraftContent,
    setEncodedFileContent,
    setFileContent,
    setFileError,
    setLoadingFilePath,
    setPreviewEncodedFileContent,
    setPreviewFileContent,
    setPreviewFilePath,
    setSelectedFile,
    setSelectedFilePathBase,
    setSelectedFileSize,
    showFileEditor,
    showHistory,
    toggleHistory,
  };
}
