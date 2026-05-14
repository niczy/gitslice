import { useCallback, useEffect, useMemo, useRef, useState } from 'react';

export function useRepoBrowserChrome({
  activeBrowserPath,
  canShowSettings,
  currentSliceLabel,
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

  const breadcrumbs = useMemo(() => {
    const slicePrefix = currentSliceLabel || 'slice';
    if (!activeBrowserPath) {
      return [{ name: slicePrefix, path: '' }];
    }
    const parts = activeBrowserPath.split('/');
    return [
      { name: slicePrefix, path: '' },
      ...parts.map((part, index) => ({
        name: part,
        path: parts.slice(0, index + 1).join('/'),
      })),
    ];
  }, [activeBrowserPath, currentSliceLabel]);

  const visibleBreadcrumbs = useMemo(() => {
    const maxBreadcrumbs = isCompactHeader ? 4 : 8;
    if (breadcrumbs.length <= maxBreadcrumbs) {
      return breadcrumbs;
    }

    const trailingCount = Math.max(maxBreadcrumbs - 2, 2);
    const ellipsisTarget = breadcrumbs[breadcrumbs.length - trailingCount - 1];
    return [
      breadcrumbs[0],
      { name: '\u2026', path: ellipsisTarget?.path || '' },
      ...breadcrumbs.slice(-trailingCount),
    ];
  }, [breadcrumbs, isCompactHeader]);

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
    visibleBreadcrumbs,
  };
}
