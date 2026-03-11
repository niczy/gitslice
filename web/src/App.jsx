import { useCallback, useEffect, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';

// Routing helpers
import { buildLegacyRedirectPath, buildPath, parseLocation } from './utils/routing.js';

// API helpers
import {
  apiBaseUrl,
  createAgentSession as createAgentSessionRequest,
  currentUsername,
  stopAgentSession as stopAgentSessionRequest,
} from './utils/api.js';
import { signInWithAccount, signOutAccount, startOAuthSignIn, startOAuthSignOut } from './auth.js';
import { useWebSession } from './hooks/useWebSession.js';
import { useSlicesQuery } from './hooks/useSlices.js';
import { useAgentCapabilitiesQuery } from './hooks/useAgentCapabilities.js';

// Components
import OverviewPage from './components/OverviewPage.jsx';
import AppHeader from './components/AppHeader.jsx';
import AppFooter from './components/AppFooter.jsx';
import AdminPage from './components/AdminPage.jsx';
import LoginPage from './components/LoginPage.jsx';
import ProfilePage from './components/ProfilePage.jsx';
import RepoBrowser from './components/RepoBrowser.jsx';
import ProjectsPage from './components/ProjectsPage.jsx';
import SettingsPage from './components/SettingsPage.jsx';
import NotFoundPage from './components/NotFoundPage.jsx';
import CommitDiffPage from './components/CommitDiffPage.jsx';
import ChangesetDiffPage from './components/ChangesetDiffPage.jsx';
import AgentSession from './components/AgentSession.jsx';
import RouteAccessState from './components/RouteAccessState.jsx';
import { trackRouteEvent } from './utils/analytics.js';
import { Button } from './components/ui/button.jsx';

// ---------------------------------------------------------------------------
// Agent Session Types
// ---------------------------------------------------------------------------

const AGENT_STATUS = {
  CREATING: 'creating',
  STARTING: 'starting',
  IDLE: 'idle',
  RUNNING: 'running',
  STOPPING: 'stopping',
  STOPPED: 'stopped',
  FAILED: 'failed',
  COMPLETED: 'completed',
  ERROR: 'error',
};

const REAL_RUNTIME_ENABLED = import.meta.env.VITE_WEB_AGENT_REAL_RUNTIME === '1';
const AGENT_PROVIDERS = ['Codex', 'Gemini', 'Claude', 'Grok', 'Kimi'];

function normalizeAgentType(value) {
  return String(value || '').trim().toLowerCase();
}

function formatAgentTypeLabel(agentType) {
  const normalized = normalizeAgentType(agentType);
  if (!normalized) return 'Agent';
  return normalized.charAt(0).toUpperCase() + normalized.slice(1);
}

function normalizeRuntimeState(value) {
  const normalized = normalizeAgentType(value);
  switch (normalized) {
    case AGENT_STATUS.CREATING:
    case AGENT_STATUS.STARTING:
    case AGENT_STATUS.RUNNING:
    case AGENT_STATUS.IDLE:
    case AGENT_STATUS.STOPPING:
    case AGENT_STATUS.STOPPED:
    case AGENT_STATUS.FAILED:
      return normalized;
    default:
      return AGENT_STATUS.RUNNING;
  }
}

function normalizeCapabilities(payload) {
  const fallback = ['codex', 'claude'];
  const supported = Array.from(new Set(
    (payload?.supportedAgentTypes || [])
      .map((value) => normalizeAgentType(value))
      .filter(Boolean),
  ));
  const supportedAgentTypes = supported.length > 0 ? supported : fallback;
  let defaultAgentType = normalizeAgentType(payload?.defaultAgentType);
  if (!supportedAgentTypes.includes(defaultAgentType)) {
    defaultAgentType = supportedAgentTypes[0];
  }
  return { supportedAgentTypes, defaultAgentType };
}

// Mock terminal lines for fake sessions
const MOCK_TERMINAL_LINES = [
  { type: 'output', text: '$ npm install' },
  { type: 'output', text: 'added 142 packages in 2.3s' },
  { type: 'output', text: '' },
  { type: 'output', text: '$ npm run build' },
  { type: 'output', text: '> gitslice@1.0.0 build' },
  { type: 'output', text: '> tsc && vite build' },
  { type: 'output', text: '' },
  { type: 'success', text: '✓ 42 modules transformed.' },
  { type: 'success', text: '✓ built in 1.23s' },
  { type: 'output', text: '' },
  { type: 'output', text: '$ gs slice status' },
  { type: 'output', text: 'Slice: auth-refresh' },
  { type: 'output', text: 'Status: ready to merge' },
  { type: 'output', text: 'Files changed: 12' },
  { type: 'prompt', text: '$ _' },
];

// ---------------------------------------------------------------------------
// Main App Component
// ---------------------------------------------------------------------------

function App() {
  const queryClient = useQueryClient();
  const initialRoute = parseLocation();
  const [activePage, setActivePage] = useState(() => initialRoute.page);
  const [diffCommitHash, setDiffCommitHash] = useState(() => initialRoute.commitHash);
  const [diffChangesetId, setDiffChangesetId] = useState(() => initialRoute.changesetId);
  const [unknownRoute, setUnknownRoute] = useState(() => initialRoute.unknownPath || '');
  const [returnToPage, setReturnToPage] = useState('browser');
  const [returnToCommitHash, setReturnToCommitHash] = useState('');
  const [returnToChangesetId, setReturnToChangesetId] = useState('');
  const githubUrl = 'https://github.com/niczy/gitslice';
  const docsUrl = 'https://github.com/niczy/gitslice/blob/main/README.md';
  const statusUrl = `${apiBaseUrl}/health`;
  const supportUrl = 'https://github.com/niczy/gitslice/issues';
  const [username, setUsername] = useState(() => currentUsername());
  const [authSessionSource, setAuthSessionSource] = useState('');
  const webSessionQuery = useWebSession();

  // Track whether the browser page has been visited so we can keep it mounted
  const [browserMounted, setBrowserMounted] = useState(() => initialRoute.page === 'browser');

  // Slice data (shared across pages)
  const [currentSliceId, setCurrentSliceId] = useState('');
  const [historyRefreshToken, setHistoryRefreshToken] = useState(0);
  const slicesQuery = useSlicesQuery();
  const slices = slicesQuery.data || [];
  const slicesLoading = slicesQuery.isLoading;
  const slicesError = slicesQuery.error ? 'Unable to load slices.' : '';

  // Agent sessions state
  const [agentSessions, setAgentSessions] = useState([]);
  const [activeSessionId, setActiveSessionId] = useState(null);
  const [isOverlayOpen, setIsOverlayOpen] = useState(false);
  const [isOverlayClosing, setIsOverlayClosing] = useState(false);
  const [selectedOverlayIndex, setSelectedOverlayIndex] = useState(0);
  const [selectedAgentType, setSelectedAgentType] = useState(() => (REAL_RUNTIME_ENABLED ? 'codex' : 'Codex'));
  const agentCapabilitiesQuery = useAgentCapabilitiesQuery(REAL_RUNTIME_ENABLED && Boolean(username));
  const agentCapabilities = normalizeCapabilities(agentCapabilitiesQuery.data || null);
  const agentCapabilitiesError = agentCapabilitiesQuery.error?.message || '';
  const [isAgentMenuOpen, setIsAgentMenuOpen] = useState(false);
  const agentMenuRef = useRef(null);
  const holdModeRef = useRef(false);
  const commandKeyDownRef = useRef(false);
  const commandHoldTimeoutRef = useRef(null);
  const COMMAND_HOLD_DELAY_MS = 600;

  // Mount the browser page once visited so it persists across navigation
  useEffect(() => {
    if (activePage === 'browser' || activePage === 'diff' || activePage === 'changeset') {
      setBrowserMounted(true);
    }
  }, [activePage]);

  const navigate = useCallback((page, commitHash = '', changesetId = '') => {
    setActivePage(page);
    if (page === 'diff') {
      setDiffCommitHash(commitHash);
      setDiffChangesetId('');
    } else if (page === 'changeset') {
      setDiffCommitHash('');
      setDiffChangesetId(changesetId);
    } else {
      setDiffCommitHash('');
      setDiffChangesetId('');
    }
    setUnknownRoute('');
    window.history.pushState(null, '', buildPath(page, commitHash, changesetId));
  }, []);

  useEffect(() => {
    const nextPath = buildLegacyRedirectPath(window.location);
    if (nextPath) {
      window.history.replaceState(null, '', nextPath);
    }
  }, []);

  // Keep app state in sync for back/forward navigation.
  useEffect(() => {
    const syncRouteFromLocation = () => {
      const { page, commitHash, changesetId, unknownPath } = parseLocation();
      setActivePage(page);
      setDiffCommitHash(commitHash);
      setDiffChangesetId(changesetId);
      setUnknownRoute(unknownPath || '');
    };
    window.addEventListener('popstate', syncRouteFromLocation);
    return () => {
      window.removeEventListener('popstate', syncRouteFromLocation);
    };
  }, []);

  useEffect(() => {
    if (slices.length === 0) {
      return;
    }
    setCurrentSliceId((prev) => {
      if (prev && slices.some((slice) => slice.slice_id === prev)) {
        return prev;
      }
      const root = slices.find((slice) => slice.is_root);
      return root ? root.slice_id : slices[0]?.slice_id || '';
    });
  }, [slices]);

  const navigateToDiff = (commitHash) => {
    navigate('diff', commitHash);
  };

  const navigateToChangesetDiff = useCallback((changesetId) => {
    navigate('changeset', '', changesetId);
  }, [navigate]);

  const navigateBackFromDiff = useCallback(() => {
    // Use history.back() to restore the previous browser URL with query params.
    // The RepoBrowser component stays mounted, so all state is preserved.
    window.history.back();
  }, []);

  const handleChangesetMerged = useCallback(() => {
    setHistoryRefreshToken((value) => value + 1);
    navigate('browser');
  }, [navigate]);

  const handleChangesetClosed = useCallback(() => {
    navigate('browser');
  }, [navigate]);

  useEffect(() => {
    const nextUsername = webSessionQuery.data?.user?.username || '';
    setUsername(nextUsername);
    setAuthSessionSource(webSessionQuery.data?.source || '');
    if (nextUsername && activePage === 'login') {
      const nextPage = returnToPage || 'browser';
      const nextCommitHash = nextPage === 'diff' ? returnToCommitHash : '';
      const nextChangesetId = nextPage === 'changeset' ? returnToChangesetId : '';
      navigate(nextPage, nextCommitHash, nextChangesetId);
    }
  }, [
    activePage,
    navigate,
    returnToChangesetId,
    returnToCommitHash,
    returnToPage,
    webSessionQuery.data,
  ]);

  const refreshSlices = useCallback(async () => {
    await slicesQuery.refetch();
  }, [slicesQuery]);

  const doLogout = useCallback(async () => {
    await signOutAccount();
    setUsername('');
    setAuthSessionSource('');
    setActivePage('landing');
    setUnknownRoute('');
    window.history.pushState(null, '', buildPath('landing', ''));
    await queryClient.invalidateQueries({ queryKey: ['web-session'] });
    if (authSessionSource === 'oauth') {
      startOAuthSignOut();
    }
  }, [authSessionSource, queryClient]);

  const doLogin = useCallback(async (nextUsername) => {
    const signedInUsername = await signInWithAccount(apiBaseUrl, nextUsername);
    setUsername(signedInUsername);
    setAuthSessionSource('dev');
    await queryClient.invalidateQueries({ queryKey: ['web-session'] });
    await queryClient.invalidateQueries({ queryKey: ['slices'] });
  }, [apiBaseUrl, queryClient]);

  const doOAuthLogin = useCallback((providerId) => {
    startOAuthSignIn(providerId);
  }, []);

  useEffect(() => {
    setSelectedAgentType((prev) => (
      agentCapabilities.supportedAgentTypes.includes(prev) ? prev : agentCapabilities.defaultAgentType
    ));
  }, [agentCapabilities]);

  const handleSessionStateChange = useCallback((sessionID, nextState) => {
    const status = normalizeRuntimeState(nextState);
    setAgentSessions((prev) => prev.map((session) => (
      session.id === sessionID ? { ...session, status } : session
    )));
  }, []);

  // Agent session handlers
  const createAgentSession = useCallback(async (provider = selectedAgentType) => {
    const currentSlice = slices.find((slice) => slice.slice_id === currentSliceId);
    if (REAL_RUNTIME_ENABLED) {
      const agentType = normalizeAgentType(provider) || agentCapabilities.defaultAgentType;
      if (!currentSliceId) {
        return;
      }
      try {
        const created = await createAgentSessionRequest({
          sliceId: currentSliceId,
          environment: currentSlice?.environment || '',
          agentType,
        });
        const sessionID = String(created?.sessionId || '').trim();
        if (!sessionID) {
          throw new Error('invalid session response');
        }
        const state = normalizeRuntimeState(created?.state);
        const nextSession = {
          id: sessionID,
          sessionId: sessionID,
          name: `${formatAgentTypeLabel(agentType)} Agent ${agentSessions.length + 1}`,
          provider: formatAgentTypeLabel(agentType),
          sliceId: String(created?.sliceId || currentSliceId),
          sliceName: currentSlice?.name || currentSliceId || 'No slice selected',
          status: state,
          createdAt: Date.now(),
          ws: created?.ws || null,
          terminalLines: [{ type: 'output', text: `Session ${sessionID} created.` }],
        };
        setAgentSessions((prev) => [...prev, nextSession]);
        setActiveSessionId(nextSession.id);
        return;
      } catch (error) {
        const nextSession = {
          id: `session-error-${Date.now()}`,
          sessionId: '',
          name: `${formatAgentTypeLabel(agentType)} Agent`,
          provider: formatAgentTypeLabel(agentType),
          sliceId: currentSliceId,
          sliceName: currentSlice?.name || currentSliceId || 'No slice selected',
          status: AGENT_STATUS.FAILED,
          createdAt: Date.now(),
          terminalLines: [{ type: 'output', text: `[error] ${error?.message || 'Unable to create agent session'}` }],
        };
        setAgentSessions((prev) => [...prev, nextSession]);
        setActiveSessionId(nextSession.id);
        return;
      }
    }

    const newSession = {
      id: `session-${Date.now()}`,
      name: `${provider} Agent ${agentSessions.length + 1}`,
      provider,
      sliceId: currentSliceId,
      sliceName: currentSlice?.name || currentSliceId || 'No slice selected',
      status: AGENT_STATUS.RUNNING,
      createdAt: Date.now(),
      terminalLines: [...MOCK_TERMINAL_LINES],
    };
    setAgentSessions((prev) => [...prev, newSession]);
    setActiveSessionId(newSession.id);
  }, [agentCapabilities.defaultAgentType, agentSessions.length, currentSliceId, selectedAgentType, slices]);

  const closeAgentSession = useCallback((sessionId, e) => {
    e?.stopPropagation();
    const closingSession = agentSessions.find((session) => session.id === sessionId);
    if (REAL_RUNTIME_ENABLED && closingSession?.sessionId) {
      stopAgentSessionRequest(closingSession.sessionId).catch(() => {
        // best effort stop; UI close should still proceed
      });
    }
    // Wait for animation to complete before removing
    setTimeout(() => {
      setAgentSessions((prev) => prev.filter((s) => s.id !== sessionId));
      if (activeSessionId === sessionId) {
        const remaining = agentSessions.filter((s) => s.id !== sessionId);
        const nextSession = remaining[0] || null;
        setActiveSessionId(nextSession ? nextSession.id : null);
        if (nextSession?.sliceId) {
          setCurrentSliceId(nextSession.sliceId);
        }
      }
    }, 250);
  }, [agentSessions, activeSessionId]);

  const selectSession = useCallback((sessionId) => {
    const selectedSession = agentSessions.find((session) => session.id === sessionId);
    setActiveSessionId(sessionId);
    if (selectedSession?.sliceId) {
      setCurrentSliceId(selectedSession.sliceId);
    }
    // Trigger closing animation
    setIsOverlayClosing(true);
    setTimeout(() => {
      setIsOverlayOpen(false);
      setIsOverlayClosing(false);
    }, 200);
  }, [agentSessions]);

  // Close overlay helper
  const closeOverlay = useCallback(() => {
    if (isOverlayOpen) {
      setIsOverlayClosing(true);
      setTimeout(() => {
        setIsOverlayOpen(false);
        setIsOverlayClosing(false);
      }, 200);
    }
  }, [isOverlayOpen]);

  const minimizeActiveSession = useCallback(() => {
    if (activeSessionId === null || isOverlayOpen) return;
    setIsFullScreenClosing(true);
    setTimeout(() => {
      setActiveSessionId(null);
      setIsFullScreenClosing(false);
    }, 300);
  }, [activeSessionId, isOverlayOpen]);

  useEffect(() => {
    if (!isOverlayOpen) {
      holdModeRef.current = false;
    }
  }, [isOverlayOpen]);

  useEffect(() => {
    if (!isAgentMenuOpen) return;
    const handleOutsideClick = (event) => {
      if (!agentMenuRef.current?.contains(event.target)) {
        setIsAgentMenuOpen(false);
      }
    };
    window.addEventListener('mousedown', handleOutsideClick);
    return () => window.removeEventListener('mousedown', handleOutsideClick);
  }, [isAgentMenuOpen]);

  useEffect(() => {
    return () => {
      if (commandHoldTimeoutRef.current) {
        clearTimeout(commandHoldTimeoutRef.current);
        commandHoldTimeoutRef.current = null;
      }
    };
  }, []);

  // Unified keyboard handling for overlay
  useEffect(() => {
    const handleKeyDown = (e) => {
      // Don't trigger if typing in input (unless it's Enter or Escape)
      const isTypingTarget = e.target.tagName === 'INPUT' || e.target.tagName === 'TEXTAREA';
      if (isTypingTarget && !e.metaKey && !e.ctrlKey && e.key !== 'Enter' && e.key !== 'Escape') {
        return;
      }

      if ((e.metaKey || e.ctrlKey) && (e.key === 'k' || e.key === 'K')) {
        e.preventDefault();
        createAgentSession();
        return;
      }

      if ((e.metaKey || e.ctrlKey) && (e.key === 'm' || e.key === 'M')) {
        if (activeSessionId !== null) {
          e.preventDefault();
          minimizeActiveSession();
        }
        return;
      }

      // Track CMD key state
      if (e.metaKey || e.ctrlKey) {
        if (!commandKeyDownRef.current) {
          commandKeyDownRef.current = true;
          if (commandHoldTimeoutRef.current) {
            clearTimeout(commandHoldTimeoutRef.current);
          }
          // CMD pressed - open overlay after a short hold
          commandHoldTimeoutRef.current = setTimeout(() => {
            if (!commandKeyDownRef.current) return;
            if (agentSessions.length > 0 && !isOverlayOpen && !holdModeRef.current) {
              holdModeRef.current = true;
              setIsOverlayOpen(true);
              // If there's an active session (maximized), select it in the carousel
              if (activeSessionId) {
                const activeIndex = agentSessions.findIndex(s => s.id === activeSessionId);
                setSelectedOverlayIndex(activeIndex >= 0 ? activeIndex : 0);
              } else {
                setSelectedOverlayIndex(0);
              }
            }
          }, COMMAND_HOLD_DELAY_MS);
        }
      }

      // Handle navigation when overlay is open
      if (isOverlayOpen) {
        if (e.key === 'ArrowLeft') {
          e.preventDefault();
          setSelectedOverlayIndex((prev) =>
            prev > 0 ? prev - 1 : agentSessions.length - 1
          );
        } else if (e.key === 'ArrowRight') {
          e.preventDefault();
          setSelectedOverlayIndex((prev) =>
            prev < agentSessions.length - 1 ? prev + 1 : 0
          );
        } else if (e.key === 'Enter') {
          e.preventDefault();
          const session = agentSessions[selectedOverlayIndex];
          if (session) {
            holdModeRef.current = false;
            selectSession(session.id);
          }
        } else if (e.key === 'Escape') {
          e.preventDefault();
          holdModeRef.current = false;
          setIsOverlayClosing(true);
          setTimeout(() => {
            setIsOverlayOpen(false);
            setIsOverlayClosing(false);
          }, 200);
        }
      }
    };

    const handleKeyUp = (e) => {
      // Check if CMD key was released
      if (!e.metaKey && !e.ctrlKey && commandKeyDownRef.current) {
        commandKeyDownRef.current = false;
        if (commandHoldTimeoutRef.current) {
          clearTimeout(commandHoldTimeoutRef.current);
          commandHoldTimeoutRef.current = null;
        }
        // If we were in hold mode and overlay is open, maximize the selected session
        if (holdModeRef.current && isOverlayOpen) {
          holdModeRef.current = false;
          const session = agentSessions[selectedOverlayIndex];
          if (session) {
            // Maximize the currently selected session
            selectSession(session.id);
          }
        }
      }
    };

    window.addEventListener('keydown', handleKeyDown);
    window.addEventListener('keyup', handleKeyUp);
    return () => {
      window.removeEventListener('keydown', handleKeyDown);
      window.removeEventListener('keyup', handleKeyUp);
    };
  }, [
    isOverlayOpen,
    agentSessions,
    selectedOverlayIndex,
    selectSession,
    activeSessionId,
    minimizeActiveSession,
    createAgentSession,
  ]);

  // Update selected index when sessions change
  useEffect(() => {
    if (selectedOverlayIndex >= agentSessions.length) {
      setSelectedOverlayIndex(Math.max(0, agentSessions.length - 1));
    }
  }, [agentSessions.length, selectedOverlayIndex]);

  const isAuthenticated = Boolean(username);
  const showAgentButton = activePage === 'browser' && (!REAL_RUNTIME_ENABLED || isAuthenticated);
  const hasAgentSessions = agentSessions.length > 0;
  const sessionsForCurrentSlice = agentSessions.filter((session) => session.sliceId === currentSliceId);
  const [isFullScreenClosing, setIsFullScreenClosing] = useState(false);
  const isFullScreenSession = !isOverlayOpen && activeSessionId !== null;
  const agentProviderOptions = REAL_RUNTIME_ENABLED
    ? (agentCapabilities.supportedAgentTypes.length > 0 ? agentCapabilities.supportedAgentTypes : ['codex', 'claude'])
    : AGENT_PROVIDERS;
  const selectedAgentLabel = REAL_RUNTIME_ENABLED ? formatAgentTypeLabel(selectedAgentType) : selectedAgentType;

  // Keep browser and diff pages on the same full-width layout to avoid visual width jumps.
  const isBrowserLayout = activePage === 'browser' || activePage === 'diff' || activePage === 'changeset';
  const isAdminUser = (username || '').toLowerCase() === 'admin';
  const blockedProtectedPages = new Set(['projects', 'settings', 'profile', 'admin']);
  const isProtectedPage = blockedProtectedPages.has(activePage);
  const hasRouteAuthorization = activePage !== 'admin' || isAdminUser;
  const routeAccessState = !isProtectedPage
    ? 'allowed'
    : !isAuthenticated
      ? 'unauthenticated'
      : hasRouteAuthorization
        ? 'allowed'
        : 'unauthorized';

  useEffect(() => {
    if (activePage === 'not-found') {
      trackRouteEvent('route_not_found', {
        path: unknownRoute || '/',
      });
      return;
    }

    if (routeAccessState !== 'allowed') {
      trackRouteEvent('route_auth_blocked', {
        page: activePage,
        reason: routeAccessState,
      });
    }
  }, [activePage, routeAccessState, unknownRoute]);

  const handleGoToLogin = useCallback(() => {
    setReturnToPage(activePage);
    setReturnToCommitHash(diffCommitHash);
    setReturnToChangesetId(diffChangesetId);
    navigate('login');
  }, [activePage, diffChangesetId, diffCommitHash, navigate]);
  const isNavActive = (item) => {
    if (item === 'repos') {
      return activePage === 'browser' || activePage === 'diff' || activePage === 'changeset';
    }
    if (item === 'projects') {
      return activePage === 'projects';
    }
    if (item === 'settings') {
      return activePage === 'settings' || activePage === 'profile';
    }
    if (item === 'login') {
      return activePage === 'login';
    }
    if (item === 'get-started') {
      return activePage === 'landing';
    }
    return false;
  };

  return (
    <div className={`app-shell min-h-screen bg-background text-foreground${isBrowserLayout ? ' app-shell--browser' : ''}`}>
      <AppHeader
        isAuthenticated={isAuthenticated}
        username={username}
        githubUrl={githubUrl}
        navigate={navigate}
        onLogout={doLogout}
        isNavActive={isNavActive}
      />

      <main className={`page${isBrowserLayout ? ' page--browser' : ''}`}>
        {activePage === 'landing' && <OverviewPage onBrowseRepo={() => navigate('browser')} />}
        {activePage === 'login' && (
          <LoginPage
            onLogin={doLogin}
            onOAuthLogin={doOAuthLogin}
            onCancel={() => navigate('landing')}
            onLoggedIn={() => {
              const nextPage = returnToPage || 'browser';
              const nextCommitHash = nextPage === 'diff' ? returnToCommitHash : '';
              const nextChangesetId = nextPage === 'changeset' ? returnToChangesetId : '';
              navigate(nextPage, nextCommitHash, nextChangesetId);
            }}
          />
        )}
        {activePage === 'projects' && routeAccessState === 'allowed' && (
          <ProjectsPage
            slices={slices}
            slicesLoading={slicesLoading}
            slicesError={slicesError}
            onOpenRepos={() => navigate('browser')}
            onRefresh={refreshSlices}
          />
        )}
        {activePage === 'profile' && routeAccessState === 'allowed' && (
          <ProfilePage username={username} onLogout={doLogout} onRequireLogin={() => navigate('login')} />
        )}
        {activePage === 'settings' && routeAccessState === 'allowed' && (
          <SettingsPage
            username={username}
            authSessionSource={authSessionSource}
            onOpenProfile={() => navigate('profile')}
            onLogout={doLogout}
          />
        )}

        {/* RepoBrowser stays mounted once visited to preserve state across browser<->diff navigation */}
        {browserMounted && routeAccessState === 'allowed' && (
          <div style={activePage !== 'browser' ? { display: 'none' } : undefined}>
            <RepoBrowser
              slices={slices}
              currentSliceId={currentSliceId}
              onSliceChange={setCurrentSliceId}
              onNavigateToDiff={navigateToDiff}
              refreshHistoryToken={historyRefreshToken}
              isActive={activePage === 'browser'}
              slicesLoading={slicesLoading}
              slicesError={slicesError}
              onRefreshSlices={refreshSlices}
            />
          </div>
        )}

        {activePage === 'diff' && routeAccessState === 'allowed' && (
          <CommitDiffPage
            commitHash={diffCommitHash}
            onBack={navigateBackFromDiff}
            onOpenChangesetDiff={navigateToChangesetDiff}
          />
        )}

        {activePage === 'changeset' && routeAccessState === 'allowed' && (
          <ChangesetDiffPage
            changesetId={diffChangesetId}
            onBack={navigateBackFromDiff}
            onMerged={handleChangesetMerged}
            onClosed={handleChangesetClosed}
          />
        )}

        {activePage === 'admin' && routeAccessState === 'allowed' && <AdminPage />}

        {isProtectedPage && routeAccessState !== 'allowed' && (
          <RouteAccessState
            state={routeAccessState}
            onGoToLogin={handleGoToLogin}
          />
        )}

        {activePage === 'not-found' && <NotFoundPage unknownPath={unknownRoute} onGoHome={() => navigate('landing')} />}
      </main>

      {showAgentButton && (
        <div className="agent-launcher" ref={agentMenuRef}>
          <Button
            type="button"
            size="sm"
            className="agent-start-btn"
            onClick={() => {
              setIsAgentMenuOpen(false);
              createAgentSession(selectedAgentType);
            }}
            title="Start new agent session (Cmd+K)"
          >
            <span className="agent-icon">🤖</span>
            <span className="agent-text">{selectedAgentLabel}</span>
          </Button>
          <Button
            type="button"
            size="sm"
            variant="secondary"
            className="agent-provider-toggle"
            aria-label="Choose agent provider"
            aria-expanded={isAgentMenuOpen}
            onClick={() => setIsAgentMenuOpen((prev) => !prev)}
          >
            <span className={`agent-provider-arrow${isAgentMenuOpen ? ' open' : ''}`}>▾</span>
          </Button>
          {isAgentMenuOpen && (
            <div className="agent-provider-menu">
              {agentProviderOptions.map((providerValue) => {
                const value = REAL_RUNTIME_ENABLED ? normalizeAgentType(providerValue) : providerValue;
                const label = REAL_RUNTIME_ENABLED ? formatAgentTypeLabel(value) : providerValue;
                return (
                  <Button
                    key={value}
                    type="button"
                    variant="ghost"
                    size="sm"
                    className={`agent-provider-item${value === selectedAgentType ? ' active' : ''}`}
                    onClick={() => {
                      setSelectedAgentType(value);
                      setIsAgentMenuOpen(false);
                    }}
                  >
                    {label}
                  </Button>
                );
              })}
              {REAL_RUNTIME_ENABLED && agentCapabilitiesError && (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="agent-provider-item"
                  disabled
                  title={agentCapabilitiesError}
                >
                  Capability fallback active
                </Button>
              )}
            </div>
          )}
        </div>
      )}

      {/* Agent Sessions Overlay */}
      {(isOverlayOpen || isOverlayClosing) && hasAgentSessions && (
        <div className={`agent-overlay ${isOverlayClosing ? 'closing' : ''}`}>
          <div className="agent-overlay-backdrop" />
          <div className="agent-overlay-content">
            <div className="agent-carousel">
              {agentSessions.map((session, index) => (
                <div
                  key={session.id}
                  className={`agent-carousel-item ${index === selectedOverlayIndex ? 'selected' : ''}`}
                  onClick={() => selectSession(session.id)}
                  style={{
                    transform: `translateX(${(index - selectedOverlayIndex) * 320}px) scale(${index === selectedOverlayIndex ? 1.05 : 0.85})`,
                    zIndex: index === selectedOverlayIndex ? 10 : 5 - Math.abs(index - selectedOverlayIndex),
                    opacity: Math.abs(index - selectedOverlayIndex) > 2 ? 0 : 1,
                  }}
                >
                  <div className="agent-carousel-header">
                    <div className="agent-carousel-meta">
                      <span className="agent-carousel-name">{session.name}</span>
                      {session.sliceName && <span className="agent-carousel-slice">{session.sliceName}</span>}
                    </div>
                    <span className={`agent-carousel-status status-${session.status}`}>
                      {session.status}
                    </span>
                  </div>
                  <div className="agent-carousel-preview">
                    <div className="agent-terminal-mock">
                      {(session.terminalLines || []).slice(0, 6).map((line, i) => (
                        <div key={i} className={`terminal-line terminal-${line.type}`}>
                          {line.text}
                        </div>
                      ))}
                    </div>
                  </div>
                </div>
              ))}
            </div>
            <div className="agent-overlay-hint">
              Hold Cmd to open, release to select. Use ← → to navigate, Enter to select.
            </div>
          </div>
        </div>
      )}

      {/* Full-screen Agent Session */}
      {(isFullScreenSession || isFullScreenClosing) && (
        <div className={`agent-fullscreen ${isFullScreenClosing ? 'closing' : ''}`}>
          <AgentSession
            session={agentSessions.find(s => s.id === activeSessionId)}
            sessions={sessionsForCurrentSlice}
            activeSessionId={activeSessionId}
            onSelectSession={selectSession}
            onClose={minimizeActiveSession}
            onCloseSession={closeAgentSession}
            onMinimize={minimizeActiveSession}
            realRuntimeEnabled={REAL_RUNTIME_ENABLED}
            onSessionStateChange={handleSessionStateChange}
          />
        </div>
      )}

      <AppFooter docsUrl={docsUrl} statusUrl={statusUrl} supportUrl={supportUrl} githubUrl={githubUrl} />
    </div>
  );
}

export default App;
