import { useCallback, useEffect, useRef, useState } from 'react';

export function useRepoBrowserChrome({
  isCompactHeader,
}) {
  const [isActionMenuOpen, setIsActionMenuOpen] = useState(false);
  const actionMenuRef = useRef(null);

  const openFilesView = useCallback(() => {}, []);

  const closeCompactActions = useCallback(() => {
    setIsActionMenuOpen(false);
  }, []);

  const toggleActionMenu = useCallback(() => {
    setIsActionMenuOpen((value) => !value);
  }, []);

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
    toggleActionMenu,
  };
}
