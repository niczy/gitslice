import { useCallback, useEffect, useRef, useState } from 'react';

export function useRepoBrowserChrome({
  canShowSettings,
  isCompactHeader,
  sliceHash,
  sliceId,
}) {
  const [isSettingsOpen, setIsSettingsOpen] = useState(false);
  const [isActionMenuOpen, setIsActionMenuOpen] = useState(false);
  const actionMenuRef = useRef(null);
  const viewingSettings = isSettingsOpen && canShowSettings;

  const openFilesView = useCallback(() => {
    setIsSettingsOpen(false);
  }, []);

  const openSettingsView = useCallback(() => {
    setIsSettingsOpen(true);
  }, []);

  const closeCompactActions = useCallback(() => {
    setIsActionMenuOpen(false);
  }, []);

  const toggleActionMenu = useCallback(() => {
    setIsActionMenuOpen((value) => !value);
  }, []);

  useEffect(() => {
    if (!canShowSettings && isSettingsOpen) {
      setIsSettingsOpen(false);
    }
  }, [canShowSettings, isSettingsOpen]);

  useEffect(() => {
    setIsSettingsOpen(false);
  }, [sliceId, sliceHash]);

  useEffect(() => {
    if (!viewingSettings || typeof window === 'undefined') {
      return undefined;
    }
    const handleKeyDown = (event) => {
      if (event.key === 'Escape') {
        setIsSettingsOpen(false);
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [viewingSettings]);

  useEffect(() => {
    if (!isCompactHeader) {
      setIsActionMenuOpen(false);
    }
  }, [isCompactHeader]);

  useEffect(() => {
    if (!isActionMenuOpen) {
      return undefined;
    }
    if (typeof document === 'undefined') {
      return undefined;
    }

    const handleClickOutside = (event) => {
      if (actionMenuRef.current && !actionMenuRef.current.contains(event.target)) {
        setIsActionMenuOpen(false);
      }
    };

    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, [isActionMenuOpen]);

  return {
    actionMenuRef,
    closeCompactActions,
    isActionMenuOpen,
    openFilesView,
    openSettingsView,
    toggleActionMenu,
    viewingSettings,
  };
}
