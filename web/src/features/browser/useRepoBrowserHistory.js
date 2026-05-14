import { useCallback, useEffect, useState } from 'react';

import { fetchWithAuth } from '../../utils/api.js';
import { normalizeChange } from '../../utils/normalize.js';
import { buildBrowserFileHistoryUrl } from './browserApi.js';

export function useRepoBrowserHistory({
  apiBaseUrl,
  isActive,
  refreshHistoryToken,
  selectedFile,
  sliceId,
}) {
  const [showHistory, setShowHistory] = useState(false);
  const [fileHistory, setFileHistory] = useState([]);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [historyError, setHistoryError] = useState('');

  const buildHistoryUrl = useCallback((filePath) => {
    return buildBrowserFileHistoryUrl({
      apiBaseUrl,
      sliceId,
      filePath,
    });
  }, [apiBaseUrl, sliceId]);

  const fetchFileHistory = useCallback(async (filePath) => {
    if (!filePath) {
      return;
    }

    setHistoryLoading(true);
    setHistoryError('');

    try {
      const response = await fetchWithAuth(buildHistoryUrl(filePath));
      if (!response.ok) {
        throw new Error(`Request failed (${response.status})`);
      }
      const payload = await response.json();
      setFileHistory((payload.changes || []).map(normalizeChange));
    } catch {
      setHistoryError('Unable to load file history.');
      setFileHistory([]);
    } finally {
      setHistoryLoading(false);
    }
  }, [buildHistoryUrl]);

  const resetHistory = useCallback(() => {
    setShowHistory(false);
    setFileHistory([]);
    setHistoryError('');
  }, []);

  const toggleHistory = useCallback(() => {
    setShowHistory((current) => {
      const next = !current;
      if (next && selectedFile && fileHistory.length === 0) {
        fetchFileHistory(selectedFile);
      }
      return next;
    });
  }, [fetchFileHistory, fileHistory.length, selectedFile]);

  useEffect(() => {
    if (!isActive || !showHistory || !selectedFile || !refreshHistoryToken) {
      return;
    }
    fetchFileHistory(selectedFile);
  }, [fetchFileHistory, isActive, refreshHistoryToken, selectedFile, showHistory]);

  return {
    fileHistory,
    historyError,
    historyLoading,
    resetHistory,
    showHistory,
    toggleHistory,
  };
}
