import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';

import {
  SIDEBAR_WIDTH_DEFAULT,
  SIDEBAR_WIDTH_MAX,
  SIDEBAR_WIDTH_MIN,
  SIDEBAR_WIDTH_STORAGE_KEY,
} from './browserConstants.js';
import { clampSidebarWidth } from './browserLayout.js';

const useIsomorphicLayoutEffect = typeof window === 'undefined' ? useEffect : useLayoutEffect;

export function useBrowserSidebar() {
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [isSidebarDismissing, setIsSidebarDismissing] = useState(false);
  const [sidebarWidth, setSidebarWidth] = useState(SIDEBAR_WIDTH_DEFAULT);
  const [isResizingSidebar, setIsResizingSidebar] = useState(false);
  const [isCompactHeader, setIsCompactHeader] = useState(false);
  const sidebarResizeRef = useRef(null);
  const hasLoadedSidebarWidthRef = useRef(false);

  const openSidebar = useCallback(() => {
    setIsSidebarDismissing(false);
    setSidebarOpen(true);
  }, []);

  const closeSidebar = useCallback(() => {
    if (typeof window !== 'undefined' && window.innerWidth <= 900) {
      setIsSidebarDismissing(true);
    } else {
      setIsSidebarDismissing(false);
    }
    setSidebarOpen(false);
  }, []);

  useEffect(() => {
    if (!isSidebarDismissing || typeof window === 'undefined') {
      return undefined;
    }
    const timeoutId = window.setTimeout(() => {
      setIsSidebarDismissing(false);
    }, 280);
    return () => window.clearTimeout(timeoutId);
  }, [isSidebarDismissing]);

  useIsomorphicLayoutEffect(() => {
    if (typeof window === 'undefined') {
      return;
    }
    setSidebarOpen(window.innerWidth > 900);
    setIsCompactHeader(window.innerWidth <= 920);
    try {
      const storedWidth = window.localStorage.getItem(SIDEBAR_WIDTH_STORAGE_KEY);
      if (storedWidth !== null) {
        setSidebarWidth(clampSidebarWidth(storedWidth));
      }
    } catch {
      // Keep the default width when localStorage is unavailable.
    }
    hasLoadedSidebarWidthRef.current = true;
  }, []);

  useEffect(() => {
    if (typeof window === 'undefined' || !hasLoadedSidebarWidthRef.current) {
      return;
    }
    try {
      window.localStorage.setItem(SIDEBAR_WIDTH_STORAGE_KEY, String(sidebarWidth));
    } catch {
      // Resizing still works for the current session.
    }
  }, [sidebarWidth]);

  useEffect(() => {
    if (typeof window === 'undefined') {
      return undefined;
    }
    const handleResize = () => {
      const width = window.innerWidth;
      setIsCompactHeader(width <= 920);
      if (width > 900) {
        setIsSidebarDismissing(false);
      }
      setSidebarOpen((open) => (width > 900 ? open : false));
    };

    handleResize();
    window.addEventListener('resize', handleResize);
    return () => window.removeEventListener('resize', handleResize);
  }, []);

  const startSidebarResize = useCallback((event) => {
    if (event.button !== undefined && event.button !== 0) {
      return;
    }
    openSidebar();
    sidebarResizeRef.current = {
      startX: event.clientX,
      startWidth: sidebarWidth,
    };
    setIsResizingSidebar(true);
    event.preventDefault();
  }, [openSidebar, sidebarWidth]);

  useEffect(() => {
    if (!isResizingSidebar || typeof window === 'undefined') {
      return undefined;
    }

    const handlePointerMove = (event) => {
      const resizeState = sidebarResizeRef.current;
      if (!resizeState) {
        return;
      }
      setSidebarWidth(clampSidebarWidth(resizeState.startWidth + event.clientX - resizeState.startX));
    };

    const stopResize = () => {
      sidebarResizeRef.current = null;
      setIsResizingSidebar(false);
    };

    window.addEventListener('pointermove', handlePointerMove);
    window.addEventListener('pointerup', stopResize);
    window.addEventListener('pointercancel', stopResize);
    return () => {
      window.removeEventListener('pointermove', handlePointerMove);
      window.removeEventListener('pointerup', stopResize);
      window.removeEventListener('pointercancel', stopResize);
    };
  }, [isResizingSidebar]);

  const handleSidebarResizeKeyDown = useCallback((event) => {
    const step = event.shiftKey ? 40 : 16;
    if (event.key === 'ArrowLeft') {
      event.preventDefault();
      openSidebar();
      setSidebarWidth((width) => clampSidebarWidth(width - step));
    } else if (event.key === 'ArrowRight') {
      event.preventDefault();
      openSidebar();
      setSidebarWidth((width) => clampSidebarWidth(width + step));
    } else if (event.key === 'Home') {
      event.preventDefault();
      openSidebar();
      setSidebarWidth(SIDEBAR_WIDTH_MIN);
    } else if (event.key === 'End') {
      event.preventDefault();
      openSidebar();
      setSidebarWidth(SIDEBAR_WIDTH_MAX);
    }
  }, [openSidebar]);

  return {
    closeSidebar,
    handleSidebarResizeKeyDown,
    isCompactHeader,
    isResizingSidebar,
    isSidebarDismissing,
    openSidebar,
    sidebarOpen,
    sidebarWidth,
    startSidebarResize,
  };
}
