import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';

import {
  AGENTS_SIDEBAR_DEFAULT_WIDTH,
  AGENTS_SIDEBAR_MAX_WIDTH,
  AGENTS_SIDEBAR_MIN_WIDTH,
  SESSIONS_SIDEBAR_MOBILE_MAX_WIDTH,
} from './agentConstants.js';
import {
  clampAgentsSidebarWidth,
  readAgentsSidebarWidth,
  writeAgentsSidebarWidth,
} from './agentLayout.js';

const useIsomorphicLayoutEffect = typeof window === 'undefined' ? useEffect : useLayoutEffect;

export function useAgentSessionsSidebar() {
  const agentsLayoutRef = useRef(null);
  const [sessionsSidebarOpen, setSessionsSidebarOpen] = useState(true);
  const [sessionsSidebarDismissing, setSessionsSidebarDismissing] = useState(false);
  const [sessionsSidebarViewportSynced, setSessionsSidebarViewportSynced] = useState(false);
  const [agentsSidebarWidth, setAgentsSidebarWidth] = useState(readAgentsSidebarWidth);
  const [agentsSidebarResizing, setAgentsSidebarResizing] = useState(false);

  const sessionsSidebarVisible = sessionsSidebarOpen || sessionsSidebarDismissing;

  const openSessionsSidebar = useCallback(() => {
    setSessionsSidebarDismissing(false);
    setSessionsSidebarOpen(true);
  }, []);

  const closeSessionsSidebar = useCallback(() => {
    if (typeof window !== 'undefined' && window.innerWidth <= 900) {
      setSessionsSidebarDismissing(true);
    } else {
      setSessionsSidebarDismissing(false);
    }
    setSessionsSidebarOpen(false);
  }, []);

  const closeSessionsSidebarForMobile = useCallback(() => {
    if (typeof window !== 'undefined' && window.innerWidth <= SESSIONS_SIDEBAR_MOBILE_MAX_WIDTH) {
      closeSessionsSidebar();
    }
  }, [closeSessionsSidebar]);

  const resizeAgentsSidebar = useCallback((clientX) => {
    const rect = agentsLayoutRef.current?.getBoundingClientRect();
    if (!rect) {
      return;
    }
    const availableMaxWidth = Math.min(
      AGENTS_SIDEBAR_MAX_WIDTH,
      Math.max(AGENTS_SIDEBAR_MIN_WIDTH, rect.width - 420),
    );
    setAgentsSidebarWidth(clampAgentsSidebarWidth(clientX - rect.left, availableMaxWidth));
  }, []);

  const handleAgentsSidebarResizePointerDown = useCallback((event) => {
    if (event.button !== undefined && event.button !== 0) {
      return;
    }
    if (typeof window !== 'undefined' && window.innerWidth <= SESSIONS_SIDEBAR_MOBILE_MAX_WIDTH) {
      return;
    }
    event.preventDefault();
    resizeAgentsSidebar(event.clientX);
    setAgentsSidebarResizing(true);
  }, [resizeAgentsSidebar]);

  const handleAgentsSidebarResizeKeyDown = useCallback((event) => {
    let nextWidth = null;
    const step = event.shiftKey ? 32 : 16;
    if (event.key === 'ArrowLeft') {
      nextWidth = agentsSidebarWidth - step;
    } else if (event.key === 'ArrowRight') {
      nextWidth = agentsSidebarWidth + step;
    } else if (event.key === 'Home') {
      nextWidth = AGENTS_SIDEBAR_MIN_WIDTH;
    } else if (event.key === 'End') {
      nextWidth = AGENTS_SIDEBAR_MAX_WIDTH;
    } else if (event.key === 'Enter') {
      nextWidth = AGENTS_SIDEBAR_DEFAULT_WIDTH;
    }
    if (nextWidth === null) {
      return;
    }
    event.preventDefault();
    setAgentsSidebarWidth(clampAgentsSidebarWidth(nextWidth));
  }, [agentsSidebarWidth]);

  const resetAgentsSidebarWidth = useCallback(() => {
    setAgentsSidebarWidth(AGENTS_SIDEBAR_DEFAULT_WIDTH);
  }, []);

  useIsomorphicLayoutEffect(() => {
    if (typeof window === 'undefined') {
      return undefined;
    }
    const syncSidebarForViewport = () => {
      if (window.innerWidth > SESSIONS_SIDEBAR_MOBILE_MAX_WIDTH) {
        setSessionsSidebarDismissing(false);
        setSessionsSidebarOpen(true);
      } else {
        setSessionsSidebarOpen(false);
      }
    };
    syncSidebarForViewport();
    setSessionsSidebarViewportSynced(true);
    window.addEventListener('resize', syncSidebarForViewport);
    return () => window.removeEventListener('resize', syncSidebarForViewport);
  }, []);

  useEffect(() => {
    if (!sessionsSidebarDismissing || typeof window === 'undefined') {
      return undefined;
    }
    const timeoutId = window.setTimeout(() => {
      setSessionsSidebarDismissing(false);
    }, 280);
    return () => window.clearTimeout(timeoutId);
  }, [sessionsSidebarDismissing]);

  useEffect(() => {
    writeAgentsSidebarWidth(agentsSidebarWidth);
  }, [agentsSidebarWidth]);

  useEffect(() => {
    if (!agentsSidebarResizing || typeof window === 'undefined') {
      return undefined;
    }
    const previousCursor = document.body.style.cursor;
    const previousUserSelect = document.body.style.userSelect;
    const handlePointerMove = (event) => {
      resizeAgentsSidebar(event.clientX);
    };
    const finishResize = () => {
      setAgentsSidebarResizing(false);
    };

    document.body.style.cursor = 'col-resize';
    document.body.style.userSelect = 'none';
    window.addEventListener('pointermove', handlePointerMove);
    window.addEventListener('pointerup', finishResize);
    window.addEventListener('pointercancel', finishResize);

    return () => {
      document.body.style.cursor = previousCursor;
      document.body.style.userSelect = previousUserSelect;
      window.removeEventListener('pointermove', handlePointerMove);
      window.removeEventListener('pointerup', finishResize);
      window.removeEventListener('pointercancel', finishResize);
    };
  }, [agentsSidebarResizing, resizeAgentsSidebar]);

  return {
    agentsLayoutRef,
    agentsSidebarResizing,
    agentsSidebarWidth,
    closeSessionsSidebarForMobile,
    closeSessionsSidebar,
    handleAgentsSidebarResizeKeyDown,
    handleAgentsSidebarResizePointerDown,
    openSessionsSidebar,
    resetAgentsSidebarWidth,
    sessionsSidebarDismissing,
    sessionsSidebarOpen,
    sessionsSidebarViewportSynced,
    sessionsSidebarVisible,
  };
}
