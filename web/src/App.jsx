import { useCallback, useEffect, useRef, useState } from 'react';
import './styles.css';

// Routing helpers
import { parseHash, buildHash } from './utils/routing.js';

// API helpers
import { apiBaseUrl, currentUsername, fetchWithAuth } from './utils/api.js';
import { fetchOAuthSession, signInWithAccount, signOutAccount, startOAuthSignIn, startOAuthSignOut } from './auth.js';

// Normalization
import { normalizeSliceInfo } from './utils/normalize.js';

// Components
import OverviewPage from './components/OverviewPage.jsx';
import LoginPage from './components/LoginPage.jsx';
import ProfilePage from './components/ProfilePage.jsx';
import RepoBrowser from './components/RepoBrowser.jsx';
import CommitDiffPage from './components/CommitDiffPage.jsx';
import ChangesetDiffPage from './components/ChangesetDiffPage.jsx';
import AgentSession from './components/AgentSession.jsx';

// ---------------------------------------------------------------------------
// Agent Session Types
// ---------------------------------------------------------------------------

const AGENT_STATUS = {
  IDLE: 'idle',
  RUNNING: 'running',
  COMPLETED: 'completed',
  ERROR: 'error',
};

const AGENT_PROVIDERS = ['Codex', 'Gemini', 'Claude', 'Grok', 'Kimi'];

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
  const initialRoute = parseHash();
  const [activePage, setActivePage] = useState(() => initialRoute.page);
  const [diffCommitHash, setDiffCommitHash] = useState(() => initialRoute.commitHash);
  const [diffChangesetId, setDiffChangesetId] = useState(() => initialRoute.changesetId);
  const githubUrl = 'https://github.com/niczy/gitslice';
  const docsUrl = 'https://github.com/niczy/gitslice/blob/main/README.md';
  const statusUrl = `${apiBaseUrl}/health`;
  const supportUrl = 'https://github.com/niczy/gitslice/issues';
  const [username, setUsername] = useState(() => currentUsername());

  // Track whether the browser page has been visited so we can keep it mounted
  const [browserMounted, setBrowserMounted] = useState(() => initialRoute.page === 'browser');

  // Slice data (shared across pages)
  const [slices, setSlices] = useState([]);
  const [currentSliceId, setCurrentSliceId] = useState('');
  const [slicesLoading, setSlicesLoading] = useState(false);
  const [slicesError, setSlicesError] = useState('');
  const [historyRefreshToken, setHistoryRefreshToken] = useState(0);

  // Agent sessions state
  const [agentSessions, setAgentSessions] = useState([]);
  const [activeSessionId, setActiveSessionId] = useState(null);
  const [isOverlayOpen, setIsOverlayOpen] = useState(false);
  const [isOverlayClosing, setIsOverlayClosing] = useState(false);
  const [selectedOverlayIndex, setSelectedOverlayIndex] = useState(0);
  const [closingSessions, setClosingSessions] = useState(new Set());
  const [selectedAgentType, setSelectedAgentType] = useState('Codex');
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
    window.history.pushState(null, '', buildHash(page, commitHash, changesetId));
  }, []);

  // Handle browser back/forward buttons
  useEffect(() => {
    const onPopState = () => {
      const { page, commitHash, changesetId } = parseHash();
      setActivePage(page);
      setDiffCommitHash(commitHash);
      setDiffChangesetId(changesetId);
    };
    window.addEventListener('popstate', onPopState);
    return () => window.removeEventListener('popstate', onPopState);
  }, []);

  // Load slices on mount
  useEffect(() => {
    const loadSlices = async () => {
      setSlicesLoading(true);
      setSlicesError('');
      try {
        const response = await fetchWithAuth(`${apiBaseUrl}/v1/slices?limit=200`);
        if (!response.ok) {
          throw new Error(`Request failed (${response.status})`);
        }
        const payload = await response.json();
        const loaded = (payload.slices || []).map(normalizeSliceInfo);
        setSlices(loaded);
        // Set default slice if none selected
        setCurrentSliceId((prev) => {
          if (prev) return prev;
          const root = loaded.find((slice) => slice.is_root);
          return root ? root.slice_id : loaded[0]?.slice_id || '';
        });
      } catch (err) {
        setSlicesError('Unable to load slices.');
      } finally {
        setSlicesLoading(false);
      }
    };
    loadSlices();
  }, [username]);

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
    const syncOAuthSession = async () => {
      if (username) {
        return;
      }
      try {
        const session = await fetchOAuthSession();
        const oauthUsername = session?.user?.username || '';
        if (!oauthUsername) {
          return;
        }
        const signedInUsername = await signInWithAccount(apiBaseUrl, oauthUsername);
        setUsername(signedInUsername);
        if (activePage === 'login') {
          navigate('browser');
        }
      } catch {
        // ignore oauth session sync failures
      }
    };
    syncOAuthSession();
  }, [activePage, apiBaseUrl, navigate, username]);

  const refreshSlices = useCallback(async () => {
    setSlicesLoading(true);
    setSlicesError('');
    try {
      const response = await fetchWithAuth(`${apiBaseUrl}/v1/slices?limit=200`);
      if (!response.ok) {
        throw new Error(`Request failed (${response.status})`);
      }
      const payload = await response.json();
      const loaded = (payload.slices || []).map(normalizeSliceInfo);
      setSlices(loaded);
    } catch (err) {
      setSlicesError('Unable to load slices.');
    } finally {
      setSlicesLoading(false);
    }
  }, []);

  const doLogout = useCallback(() => {
    signOutAccount();
    setUsername('');
    setActivePage('landing');
    window.history.pushState(null, '', buildHash('landing', ''));
    startOAuthSignOut();
  }, []);

  const doLogin = useCallback(async (nextUsername) => {
    const signedInUsername = await signInWithAccount(apiBaseUrl, nextUsername);
    setUsername(signedInUsername);
  }, [apiBaseUrl]);

  const doOAuthLogin = useCallback((providerId) => {
    startOAuthSignIn(providerId);
  }, []);

  // Agent session handlers
  const createAgentSession = useCallback((provider = selectedAgentType) => {
    const currentSlice = slices.find((slice) => slice.slice_id === currentSliceId);
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
  }, [agentSessions.length, selectedAgentType, slices, currentSliceId]);

  const closeAgentSession = useCallback((sessionId, e) => {
    e?.stopPropagation();
    // Add to closing set for animation
    setClosingSessions((prev) => new Set([...prev, sessionId]));
    // Wait for animation to complete before removing
    setTimeout(() => {
      setAgentSessions((prev) => prev.filter((s) => s.id !== sessionId));
      setClosingSessions((prev) => {
        const next = new Set(prev);
        next.delete(sessionId);
        return next;
      });
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

  const showAgentButton = activePage === 'browser';
  const hasAgentSessions = agentSessions.length > 0;
  const [isFullScreenClosing, setIsFullScreenClosing] = useState(false);
  const isFullScreenSession = !isOverlayOpen && activeSessionId !== null;

  // Keep browser and diff pages on the same full-width layout to avoid visual width jumps.
  const isBrowserLayout = activePage === 'browser' || activePage === 'diff' || activePage === 'changeset';

  return (
    <div className={`app-shell${isBrowserLayout ? ' app-shell--browser' : ''}`}>
      <header className="top-bar">
        <button type="button" className="brand" onClick={() => navigate('landing')}>
          <span className="brand-icon">◆</span>
          <span className="brand-text">Git Slice</span>
        </button>
        <div className="top-bar-actions">
          {username ? (
            <>
              <button
                type="button"
                className="ghost"
                data-testid="topbar-profile"
                onClick={() => navigate('profile')}
                title="Profile"
              >
                {username}
              </button>
              <button
                type="button"
                className="ghost"
                data-testid="topbar-logout"
                onClick={doLogout}
              >
                Logout
              </button>
            </>
          ) : (
            <button
              type="button"
              className="ghost"
              data-testid="topbar-login"
              onClick={() => navigate('login')}
            >
              Login
            </button>
          )}
          <a className="ghost" href={githubUrl} target="_blank" rel="noreferrer" data-testid="topbar-github-link">
            GitHub
          </a>
          <button
            type="button"
            className="primary"
            data-testid="topbar-repo-browser"
            onClick={() => navigate('browser')}
          >
            Repo Browser
          </button>
        </div>
      </header>

      <main className={`page${isBrowserLayout ? ' page--browser' : ''}`}>
        {activePage === 'landing' && <OverviewPage onBrowseRepo={() => navigate('browser')} />}
        {activePage === 'login' && (
          <LoginPage onLogin={doLogin} onOAuthLogin={doOAuthLogin} onCancel={() => navigate('landing')} onLoggedIn={() => navigate('browser')} />
        )}
        {activePage === 'profile' && (
          <ProfilePage username={username} onLogout={doLogout} onRequireLogin={() => navigate('login')} />
        )}

        {/* RepoBrowser stays mounted once visited to preserve state across browser<->diff navigation */}
        {browserMounted && (
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

        {activePage === 'diff' && (
          <CommitDiffPage
            commitHash={diffCommitHash}
            onBack={navigateBackFromDiff}
            onOpenChangesetDiff={navigateToChangesetDiff}
          />
        )}

        {activePage === 'changeset' && (
          <ChangesetDiffPage
            changesetId={diffChangesetId}
            onBack={navigateBackFromDiff}
            onMerged={handleChangesetMerged}
            onClosed={handleChangesetClosed}
          />
        )}
      </main>

      {showAgentButton && (
        <div className="agent-launcher" ref={agentMenuRef}>
          <button
            type="button"
            className="agent-start-btn"
            onClick={() => {
              setIsAgentMenuOpen(false);
              createAgentSession(selectedAgentType);
            }}
            title="Start new agent session (Cmd+K)"
          >
            <span className="agent-icon">🤖</span>
            <span className="agent-text">{selectedAgentType}</span>
          </button>
          <button
            type="button"
            className="agent-provider-toggle"
            aria-label="Choose agent provider"
            aria-expanded={isAgentMenuOpen}
            onClick={() => setIsAgentMenuOpen((prev) => !prev)}
          >
            <span className={`agent-provider-arrow${isAgentMenuOpen ? ' open' : ''}`}>▾</span>
          </button>
          {isAgentMenuOpen && (
            <div className="agent-provider-menu">
              {AGENT_PROVIDERS.map((provider) => (
                <button
                  key={provider}
                  type="button"
                  className={`agent-provider-item${provider === selectedAgentType ? ' active' : ''}`}
                  onClick={() => {
                    setSelectedAgentType(provider);
                    setIsAgentMenuOpen(false);
                  }}
                >
                  {provider}
                </button>
              ))}
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
                      {session.terminalLines.slice(0, 6).map((line, i) => (
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
            onClose={minimizeActiveSession}
            onMinimize={minimizeActiveSession}
          />
        </div>
      )}

      {/* Minimized Agent Sessions Bar */}
      {hasAgentSessions && !isOverlayOpen && (
        <div className="agent-sessions-bar">
          {agentSessions.map((session) => (
            <div
              key={session.id}
              className={`agent-session-pill ${session.id === activeSessionId ? 'active' : ''} ${closingSessions.has(session.id) ? 'removing' : ''}`}
              onClick={() => {
                setActiveSessionId(session.id);
                if (session.sliceId) {
                  setCurrentSliceId(session.sliceId);
                }
              }}
            >
              <span className="agent-session-icon">🤖</span>
              <span className="agent-session-name">
                {session.name}
                {session.sliceName && <span className="agent-session-slice-pill">{session.sliceName}</span>}
              </span>
              <button
                type="button"
                className="agent-session-close"
                onClick={(e) => closeAgentSession(session.id, e)}
              >
                ×
              </button>
            </div>
          ))}
        </div>
      )}

      <footer className="footer" aria-label="Global footer">
        <p className="footer-copy">Git Slice • Slice smart. Ship faster.</p>
        <nav className="footer-links" aria-label="Self-service links">
          <a href={docsUrl} target="_blank" rel="noreferrer">Docs</a>
          <a href={statusUrl} target="_blank" rel="noreferrer">Status</a>
          <a href={supportUrl} target="_blank" rel="noreferrer">Support</a>
          <a href={githubUrl} target="_blank" rel="noreferrer">GitHub</a>
        </nav>
      </footer>
    </div>
  );
}

export default App;
