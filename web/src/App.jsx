import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import './styles.css';

// ---------------------------------------------------------------------------
// Hash-based routing helpers
// ---------------------------------------------------------------------------

function parseHash() {
  const hash = window.location.hash.replace(/^#\/?/, '');
  if (hash.startsWith('diff/')) {
    return { page: 'diff', commitHash: decodeURIComponent(hash.slice(5)) };
  }
  if (hash === 'login') {
    return { page: 'login', commitHash: '' };
  }
  if (hash === 'profile') {
    return { page: 'profile', commitHash: '' };
  }
  if (hash === 'browser' || hash.startsWith('browser?')) {
    return { page: 'browser', commitHash: '' };
  }
  return { page: 'landing', commitHash: '' };
}

function buildHash(page, commitHash) {
  if (page === 'diff' && commitHash) {
    return `#/diff/${encodeURIComponent(commitHash)}`;
  }
  if (page === 'login') {
    return '#/login';
  }
  if (page === 'profile') {
    return '#/profile';
  }
  if (page === 'browser') {
    return '#/browser';
  }
  return '#/';
}

const features = [
  {
    title: 'Speed',
    description:
      'Slice only what you need, reuse the rest. Move from idea to review faster with focused diffs and reproducible runs.',
  },
  {
    title: 'Safety',
    description:
      'Keep changes isolated. Guardrails make it easy to test, share, and roll back without risking the rest of your repo.',
  },
  {
    title: 'Tooling',
    description:
      'First-class CLI and services for orchestrating slices, automations, and integrations with your existing workflows.',
  },
];

const apiBaseUrl = import.meta.env.VITE_FILE_API_BASE_URL || '';

function currentUsername() {
  try {
    return window.localStorage.getItem('gs_username') || '';
  } catch {
    return '';
  }
}

function fetchWithAuth(url, options = {}) {
  const headers = new Headers(options.headers || {});
  const username = currentUsername();
  if (username) {
    headers.set('Authorization', `User ${username}`);
  }
  return fetch(url, { ...options, headers });
}

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
  const [activePage, setActivePage] = useState(() => parseHash().page);
  const [diffCommitHash, setDiffCommitHash] = useState(() => parseHash().commitHash);
  const [browserKey, setBrowserKey] = useState(0);
  const githubUrl = 'https://github.com/niczy/gitslice';
  const [username, setUsername] = useState(() => currentUsername());

  // Slice data (shared across pages)
  const [slices, setSlices] = useState([]);
  const [currentSliceId, setCurrentSliceId] = useState('');
  const [slicesLoading, setSlicesLoading] = useState(false);
  const [slicesError, setSlicesError] = useState('');

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

  const navigate = useCallback((page, commitHash = '') => {
    setActivePage(page);
    if (page === 'diff') {
      setDiffCommitHash(commitHash);
    }
    window.history.pushState(null, '', buildHash(page, commitHash));
  }, []);

  // Handle browser back/forward buttons
  useEffect(() => {
    const onPopState = () => {
      const { page, commitHash } = parseHash();
      setActivePage(page);
      setDiffCommitHash(commitHash);
      if (page === 'browser') {
        setBrowserKey((k) => k + 1);
      }
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
    try {
      window.localStorage.removeItem('gs_username');
    } catch {
      // ignore
    }
    setUsername('');
    setActivePage('landing');
    window.history.pushState(null, '', buildHash('landing', ''));
  }, []);

  const doLogin = useCallback(async (nextUsername) => {
    const trimmed = (nextUsername || '').trim();
    const response = await fetch(`${apiBaseUrl}/v1/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username: trimmed }),
    });
    if (!response.ok) {
      throw new Error('Invalid username');
    }
    try {
      window.localStorage.setItem('gs_username', trimmed);
    } catch {
      // ignore
    }
    setUsername(trimmed);
  }, []);

  // Agent session handlers
  const createAgentSession = useCallback((provider = selectedAgentType) => {
    const newSession = {
      id: `session-${Date.now()}`,
      name: `${provider} Agent ${agentSessions.length + 1}`,
      provider,
      status: AGENT_STATUS.RUNNING,
      createdAt: Date.now(),
      terminalLines: [...MOCK_TERMINAL_LINES],
    };
    setAgentSessions((prev) => [...prev, newSession]);
    setActiveSessionId(newSession.id);
  }, [agentSessions.length, selectedAgentType]);

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
        setActiveSessionId(remaining.length > 0 ? remaining[0].id : null);
      }
    }, 250);
  }, [agentSessions, activeSessionId]);

  const selectSession = useCallback((sessionId) => {
    setActiveSessionId(sessionId);
    // Trigger closing animation
    setIsOverlayClosing(true);
    setTimeout(() => {
      setIsOverlayOpen(false);
      setIsOverlayClosing(false);
    }, 200);
  }, []);

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

  return (
    <div className={`app-shell${activePage === 'browser' ? ' app-shell--browser' : ''}`}>
      <header className="top-bar">
        <button type="button" className="brand" onClick={() => navigate('landing')}>
          <span className="brand-icon">◆</span>
          <span className="brand-text">Git Slice</span>
        </button>
        <div className="top-bar-actions">
          {activePage === 'browser' && (
            <SliceDropdown
              slices={slices}
              currentSliceId={currentSliceId}
              onSelectSlice={setCurrentSliceId}
              loading={slicesLoading}
              error={slicesError}
              onRefresh={refreshSlices}
            />
          )}
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

      <main className={`page${activePage === 'browser' ? ' page--browser' : ''}`}>
        {activePage === 'landing' && <OverviewPage onBrowseRepo={() => navigate('browser')} />}
        {activePage === 'login' && (
          <LoginPage onLogin={doLogin} onCancel={() => navigate('landing')} onLoggedIn={() => navigate('browser')} />
        )}
        {activePage === 'profile' && (
          <ProfilePage username={username} onLogout={doLogout} onRequireLogin={() => navigate('login')} />
        )}
        {activePage === 'browser' && (
          <RepoBrowser
            key={browserKey}
            slices={slices}
            currentSliceId={currentSliceId}
            onSliceChange={setCurrentSliceId}
            onNavigateToDiff={navigateToDiff}
          />
        )}
        {activePage === 'diff' && (
          <CommitDiffPage commitHash={diffCommitHash} onBack={() => navigate('browser')} />
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
                    <span className="agent-carousel-name">{session.name}</span>
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
              onClick={() => setActiveSessionId(session.id)}
            >
              <span className="agent-session-icon">🤖</span>
              <span className="agent-session-name">{session.name}</span>
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

      <footer className="footer">
        <p>
          Git Slice • Slice smart. Ship faster. •{' '}
          <a href={githubUrl} target="_blank" rel="noreferrer">
            GitHub
          </a>
        </p>
      </footer>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Agent Session Component
// ---------------------------------------------------------------------------

function AgentSession({ session, onClose, onMinimize }) {
  const [inputValue, setInputValue] = useState('');
  const [lines, setLines] = useState([]);
  const [displayedLines, setDisplayedLines] = useState(0);
  const [isProcessing, setIsProcessing] = useState(false);
  const terminalRef = useRef(null);

  // Staggered loading animation for initial lines
  useEffect(() => {
    if (session?.terminalLines) {
      setLines(session.terminalLines);
      setDisplayedLines(0);
      let index = 0;
      const interval = setInterval(() => {
        index++;
        setDisplayedLines(index);
        if (index >= session.terminalLines.length) {
          clearInterval(interval);
        }
      }, 50); // 50ms between each line appearing
      return () => clearInterval(interval);
    }
  }, [session?.id]); // Reset when session changes

  useEffect(() => {
    // Scroll to bottom when lines change
    if (terminalRef.current) {
      terminalRef.current.scrollTop = terminalRef.current.scrollHeight;
    }
  }, [lines, displayedLines]);

  const handleSubmit = async (e) => {
    e.preventDefault();
    if (!inputValue.trim() || isProcessing) return;

    setIsProcessing(true);
    const command = inputValue;
    
    // Remove the prompt line and add the command
    setLines((prev) => [
      ...prev.slice(0, -1),
      { type: 'prompt', text: `$ ${command}` },
    ]);
    setInputValue('');

    // Simulate processing delay with intermediate outputs
    await new Promise((r) => setTimeout(r, 400));
    setLines((prev) => [...prev, { type: 'output', text: 'Processing command...' }]);
    
    await new Promise((r) => setTimeout(r, 500));
    setLines((prev) => [...prev, { type: 'output', text: `Executing: ${command}` }]);
    
    await new Promise((r) => setTimeout(r, 600));
    
    // Add result based on command
    const results = [
      { type: 'success', text: '✓ Command executed successfully' },
      { type: 'output', text: `Output: ${Math.floor(Math.random() * 1000)} items processed` },
      { type: 'output', text: 'Time: 0.42s' },
    ];
    
    setLines((prev) => [...prev, ...results, { type: 'prompt', text: '$ _' }]);
    setIsProcessing(false);
  };

  if (!session) return null;

  // Only show lines up to the displayed count for initial animation
  const visibleLines = lines.slice(0, Math.max(displayedLines, lines.length - 1));

  return (
    <div className="agent-session-container">
      <div className="agent-session-header">
        <div className="agent-session-title">
          <span className="agent-session-title-icon">🤖</span>
          <span>{session.name}</span>
          <span className={`agent-status-badge status-${session.status}`}>
            {session.status}
          </span>
        </div>
        <div className="agent-session-actions">
          <button
            type="button"
            className="agent-session-action-btn"
            onClick={onMinimize}
            title="Minimize (Cmd+M)"
          >
            −
          </button>
          <button
            type="button"
            className="agent-session-action-btn"
            onClick={onClose}
            title="Close"
          >
            ×
          </button>
        </div>
      </div>
      <div className="agent-session-body">
        <div className="agent-terminal" ref={terminalRef}>
          {visibleLines.map((line, index) => (
            <div 
              key={index} 
              className={`terminal-line terminal-${line.type}`}
              style={{ animationDelay: `${index * 30}ms` }}
            >
              {line.type === 'prompt' && (
                <span className="terminal-prompt">$ </span>
              )}
              <span className={`terminal-text terminal-text-${line.type}`}>
                {line.text.replace(/^\$ /, '').replace(/_$/, '')}
                {line.text.endsWith('_') && <span className="terminal-cursor">_</span>}
              </span>
            </div>
          ))}
          {isProcessing && (
            <div className="terminal-line terminal-output">
              <span className="terminal-processing">
                <span className="processing-dot">.</span>
                <span className="processing-dot">.</span>
                <span className="processing-dot">.</span>
              </span>
            </div>
          )}
        </div>
        <form className="agent-input-form" onSubmit={handleSubmit}>
          <span className="agent-input-prompt">$</span>
          <input
            type="text"
            className="agent-input"
            value={inputValue}
            onChange={(e) => setInputValue(e.target.value)}
            placeholder={isProcessing ? 'Processing...' : 'Type a command...'}
            autoFocus
            disabled={isProcessing}
          />
        </form>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Slice Dropdown Component
// ---------------------------------------------------------------------------

function SliceDropdown({ slices, currentSliceId, onSelectSlice, loading, error, onRefresh }) {
  const [isOpen, setIsOpen] = useState(false);
  const [filter, setFilter] = useState('');
  const dropdownRef = useRef(null);

  const currentSlice = useMemo(() =>
    slices.find((s) => s.slice_id === currentSliceId),
    [slices, currentSliceId]
  );

  const filteredSlices = useMemo(() => {
    const query = filter.trim().toLowerCase();
    if (!query) return slices;
    return slices.filter((slice) => {
      const name = (slice.name || slice.slice_id || '').toLowerCase();
      const description = (slice.description || '').toLowerCase();
      return name.includes(query) || description.includes(query);
    });
  }, [filter, slices]);

  // Close dropdown when clicking outside
  useEffect(() => {
    const handleClickOutside = (event) => {
      if (dropdownRef.current && !dropdownRef.current.contains(event.target)) {
        setIsOpen(false);
      }
    };
    if (isOpen) {
      document.addEventListener('mousedown', handleClickOutside);
    }
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, [isOpen]);

  const handleSelect = (sliceId) => {
    onSelectSlice(sliceId);
    setIsOpen(false);
    setFilter('');
  };

  return (
    <div className="slice-dropdown" ref={dropdownRef}>
      <button
        type="button"
        className="slice-dropdown-trigger"
        onClick={() => setIsOpen(!isOpen)}
        data-testid="slice-dropdown-trigger"
      >
        <span>{currentSlice ? (currentSlice.name || currentSlice.slice_id) : 'Select slice'}</span>
        <span className="slice-dropdown-arrow">{isOpen ? '▲' : '▼'}</span>
      </button>
      {isOpen && (
        <div className="slice-dropdown-menu" data-testid="slice-dropdown-menu">
          <div className="slice-dropdown-header">
            <h4>Slices</h4>
            <div className="slice-search">
              <input
                type="text"
                placeholder="Filter slices..."
                value={filter}
                onChange={(e) => setFilter(e.target.value)}
                data-testid="slice-dropdown-filter"
                autoFocus
              />
            </div>
          </div>
          <ul className="slice-dropdown-list" data-testid="slice-list">
            {loading && (
              <li className="slice-dropdown-loading">Loading…</li>
            )}
            {error && (
              <li className="slice-dropdown-error">
                {error}
                <button type="button" onClick={onRefresh} style={{ marginLeft: '8px' }}>
                  Retry
                </button>
              </li>
            )}
            {!loading && !error && filteredSlices.length === 0 && (
              <li className="slice-dropdown-empty">No slices found.</li>
            )}
            {!loading && !error && filteredSlices.map((slice) => (
              <li key={slice.slice_id}>
                <button
                  type="button"
                  className={`slice-dropdown-item ${currentSliceId === slice.slice_id ? 'active' : ''}`}
                  onClick={() => handleSelect(slice.slice_id)}
                  data-testid="slice-dropdown-item"
                >
                  <div className="slice-dropdown-item-title">
                    <span className="slice-name">{slice.name || slice.slice_id}</span>
                    {slice.is_root && <span className="slice-badge">root</span>}
                  </div>
                  <div className="slice-dropdown-item-meta">
                    <span>{slice.slice_id}</span>
                    {typeof slice.file_count === 'number' && (
                      <span>{slice.file_count} files</span>
                    )}
                  </div>
                  {slice.description && (
                    <div style={{ fontSize: '0.75rem', color: 'var(--text-muted)', marginTop: '2px' }}>
                      {slice.description}
                    </div>
                  )}
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Overview Page Component
// ---------------------------------------------------------------------------

function OverviewPage({ onBrowseRepo }) {
  return (
    <>
      <section className="hero">
        <div className="hero-content">
          <p className="eyebrow">Introducing Git Slice</p>
          <h1>Slice-based workflows for shipping more confidently.</h1>
          <p className="lede">
            Git Slice lets teams carve out focused slices of work, run them end-to-end, and merge back with clarity. Each slice is
            stored in the slice service, so teams can standardize how a given area of the repo is scoped and tested.
          </p>
          <div className="cta-row">
            <button type="button" className="primary" onClick={onBrowseRepo}>
              Open repo browser
            </button>
            <a className="ghost" href="mailto:team@gitslice.dev">
              Contact the team
            </a>
          </div>
        </div>
        <div className="hero-panel">
          <div className="hero-card">
            <p className="eyebrow">Slice-first development</p>
            <h2>Run isolated slices from idea to production</h2>
            <p>
              Define a slice around a task, pull the dependencies you need, and keep every change traceable. Git Slice keeps
              delivery focused so teams can move without long-lived branches.
            </p>
          </div>
        </div>
      </section>

      <section id="overview" className="section">
        <div className="section-header">
          <h2>How slices keep changes focused</h2>
          <p>
            A slice captures only the files and services you specify, plus any required dependencies. That means slimmer clones,
            deterministic test runs, and a clean diff that can be merged without dragging unrelated changes along for the ride.
          </p>
        </div>
        <div className="steps">
          <div className="step">
            <div className="step-number">1</div>
            <div>
              <h3>Carve out the slice</h3>
              <p>Define the slice in the service with the immutable set of files, directories, and services required for the task.</p>
            </div>
          </div>
          <div className="step">
            <div className="step-number">2</div>
            <div>
              <h3>Iterate quickly</h3>
              <p>Use the CLI to check out the slice by its slice ID and run targeted tests.</p>
            </div>
          </div>
          <div className="step">
            <div className="step-number">3</div>
            <div>
              <h3>Merge with confidence</h3>
              <p>Every slice ships with reproducible logs, checks, and diffs so merging back is predictable and low-risk.</p>
            </div>
          </div>
        </div>
      </section>

      <section className="section quickstart">
        <div className="section-header">
          <p className="eyebrow">Quick start</p>
          <h2>Go from repo to slice in minutes</h2>
          <p>Use the CLI to check out a slice using its slice ID.</p>
        </div>
        <div className="quickstart-grid">
          <div className="quickstart-step">
            <h3>1. Create a slice</h3>
            <pre className="code-block">
              <code>gs fork auth-refresh ./services/auth --parent root_slice</code>
            </pre>
            <p>Store the slice definition in the service and reference it by a stable slice ID.</p>
          </div>
          <div className="quickstart-step">
            <h3>2. Check out the slice</h3>
            <pre className="code-block">
              <code>gs slice checkout auth-refresh</code>
            </pre>
            <p>Use the slice ID to pull just the required scope into a local workspace.</p>
          </div>
          <div className="quickstart-step">
            <h3>3. Validate the slice</h3>
            <pre className="code-block">
              <code>gs slice checkout auth-refresh --commit HEAD</code>
            </pre>
            <p>Pin a commit when needed so the slice scope is reproducible for reviewers and CI.</p>
          </div>
        </div>
      </section>

      <section id="features" className="section features">
        <div className="section-header">
          <p className="eyebrow">Built for teams</p>
          <h2>Feature highlights</h2>
          <p>Everything you need to move fast without losing control.</p>
        </div>
        <div className="feature-grid">
          {features.map((feature) => (
            <div key={feature.title} className="feature">
              <h3>{feature.title}</h3>
              <p>{feature.description}</p>
            </div>
          ))}
        </div>
      </section>

      <section className="section cta">
        <div>
          <p className="eyebrow">Ready to slice?</p>
          <h2>Bring slice-based delivery to your team.</h2>
          <p>Start with the CLI and wire it into your CI/CD. Git Slice is built to plug into your existing workflows.</p>
        </div>
        <a className="primary" href="mailto:team@gitslice.dev">
          Contact the team
        </a>
      </section>
    </>
  );
}

// ---------------------------------------------------------------------------
// Login / Profile
// ---------------------------------------------------------------------------

function LoginPage({ onLogin, onLoggedIn, onCancel }) {
  const [value, setValue] = useState(() => currentUsername());
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  return (
    <section className="section auth-page" data-testid="login-page">
      <div className="section-header">
        <p className="eyebrow">Accounts</p>
        <h2>Login</h2>
        <p>Pick a username. This is fake auth: no password.</p>
      </div>

      <form
        className="auth-card"
        onSubmit={async (e) => {
          e.preventDefault();
          setError('');
          setLoading(true);
          try {
            await onLogin(value);
            onLoggedIn?.();
          } catch (err) {
            setError('Invalid username. Use 3-32 chars: letters, numbers, "_" or "-", starting with a letter/number.');
          } finally {
            setLoading(false);
          }
        }}
      >
        <label className="field">
          <span className="field-label">Username</span>
          <input
            type="text"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            placeholder="e.g. nic"
            autoFocus
            spellCheck={false}
            autoComplete="off"
          />
        </label>
        {error && <div className="panel-error">{error}</div>}
        <div className="auth-actions">
          <button type="submit" className="primary" disabled={loading}>
            {loading ? 'Logging in…' : 'Login'}
          </button>
          <button type="button" className="ghost" onClick={onCancel}>
            Cancel
          </button>
        </div>
      </form>
    </section>
  );
}

function ProfilePage({ username, onLogout, onRequireLogin }) {
  const [me, setMe] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [orgName, setOrgName] = useState('');
  const [orgCreating, setOrgCreating] = useState(false);
  const [orgError, setOrgError] = useState('');

  const loadMe = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const resp = await fetchWithAuth(`${apiBaseUrl}/v1/me`);
      if (resp.status === 401) {
        onRequireLogin?.();
        return;
      }
      if (!resp.ok) throw new Error('bad');
      setMe(await resp.json());
    } catch {
      setError('Unable to load profile.');
    } finally {
      setLoading(false);
    }
  }, [onRequireLogin]);

  useEffect(() => {
    loadMe();
  }, [loadMe]);

  return (
    <section className="section auth-page" data-testid="profile-page">
      <div className="section-header">
        <p className="eyebrow">Accounts</p>
        <h2>Profile</h2>
        <p>{username ? `Logged in as ${username}.` : 'Not logged in.'}</p>
      </div>

      <div className="profile-grid">
        <div className="auth-card">
          <h3 style={{ marginTop: 0 }}>Account</h3>
          {loading && <div className="status">Loading…</div>}
          {error && <div className="panel-error">{error}</div>}
          {!loading && !error && me?.user && (
            <div className="kv">
              <div className="kv-row">
                <span className="kv-key">Username</span>
                <span className="kv-val">{me.user.username}</span>
              </div>
              <div className="kv-row">
                <span className="kv-key">Created</span>
                <span className="kv-val">
                  {me.user.created_at ? new Date(me.user.created_at).toLocaleString() : 'unknown'}
                </span>
              </div>
            </div>
          )}
          <div className="auth-actions" style={{ marginTop: '16px' }}>
            <button type="button" className="ghost" onClick={loadMe}>
              Refresh
            </button>
            <button type="button" className="ghost" onClick={onLogout}>
              Logout
            </button>
          </div>
        </div>

        <div className="auth-card">
          <h3 style={{ marginTop: 0 }}>Organizations</h3>
          {!loading && !error && (me?.organizations || []).length === 0 && (
            <div className="panel-empty">No organizations yet.</div>
          )}
          {!loading && !error && (me?.organizations || []).length > 0 && (
            <ul className="org-list">
              {(me.organizations || []).map((o) => (
                <li key={o.slug} className="org-item">
                  <div className="org-item-title">
                    <span className="org-name">{o.name}</span>
                    <span className="org-slug">{o.slug}</span>
                  </div>
                  <div className="org-item-meta">Created by {o.created_by || 'unknown'}</div>
                </li>
              ))}
            </ul>
          )}

          <form
            className="org-create"
            onSubmit={async (e) => {
              e.preventDefault();
              setOrgError('');
              setOrgCreating(true);
              try {
                const resp = await fetchWithAuth(`${apiBaseUrl}/v1/orgs`, {
                  method: 'POST',
                  headers: { 'Content-Type': 'application/json' },
                  body: JSON.stringify({ name: orgName }),
                });
                if (!resp.ok) throw new Error('bad');
                setOrgName('');
                await loadMe();
              } catch {
                setOrgError('Unable to create organization.');
              } finally {
                setOrgCreating(false);
              }
            }}
          >
            <label className="field">
              <span className="field-label">New organization</span>
              <input
                type="text"
                value={orgName}
                onChange={(e) => setOrgName(e.target.value)}
                placeholder="e.g. Acme"
              />
            </label>
            {orgError && <div className="panel-error">{orgError}</div>}
            <div className="auth-actions">
              <button type="submit" className="primary" disabled={orgCreating || !orgName.trim()}>
                {orgCreating ? 'Creating…' : 'Create organization'}
              </button>
            </div>
          </form>
        </div>
      </div>
    </section>
  );
}

// ---------------------------------------------------------------------------
// Repo Browser Component
// ---------------------------------------------------------------------------
function RepoBrowser({ slices, currentSliceId, onSliceChange, onNavigateToDiff }) {
  // Parse initial browser state from URL hash on mount
  const initialBrowserState = useMemo(() => {
    const raw = window.location.hash.replace(/^#\/?/, '');
    if (raw.startsWith('browser?')) {
      const params = new URLSearchParams(raw.slice(raw.indexOf('?') + 1));
      return {
        file: params.get('file') || '',
        slice: params.get('slice') || '',
        sliceHash: params.get('sliceHash') || '',
      };
    }
    return null;
  }, []);

  const [sliceHash, setSliceHash] = useState(initialBrowserState?.sliceHash || '');
  const [treeEntries, setTreeEntries] = useState({});
  const [expandedPaths, setExpandedPaths] = useState(['']);
  const [selectedFile, setSelectedFile] = useState(null);
  const [fileContent, setFileContent] = useState('');
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState('');
  const [sidebarOpen, setSidebarOpen] = useState(() => window.innerWidth > 900);
  const [showHistory, setShowHistory] = useState(false);
  const [fileHistory, setFileHistory] = useState([]);
  const [historyLoading, setHistoryLoading] = useState(false);
  const [historyError, setHistoryError] = useState('');

  // File to restore after root tree entries load (from URL hash)
  const pendingFileRef = useRef(initialBrowserState?.file || null);
  const highlightedContent = useMemo(() => highlightCode(fileContent), [fileContent]);

  const breadcrumbs = useMemo(() => {
    if (!selectedFile) {
      return [{ name: 'root', path: '' }];
    }
    const parts = selectedFile.split('/');
    return [
      { name: 'root', path: '' },
      ...parts.map((part, index) => ({
        name: part,
        path: parts.slice(0, index + 1).join('/'),
      })),
    ];
  }, [selectedFile]);

  const sliceId = currentSliceId;

  // Update slice from URL if present
  useEffect(() => {
    if (initialBrowserState?.slice && initialBrowserState.slice !== currentSliceId) {
      const sliceExists = slices.some(s => s.slice_id === initialBrowserState.slice);
      if (sliceExists) {
        onSliceChange(initialBrowserState.slice);
      }
    }
  }, [initialBrowserState, slices, currentSliceId, onSliceChange]);

  // Reset tree when slice changes
  useEffect(() => {
    setTreeEntries({});
    setExpandedPaths(['']);
    setSelectedFile(null);
    setFileContent('');
  }, [sliceId, sliceHash]);

  const encodePath = (value) => value.split('/').map(encodeURIComponent).join('/');

  // Build URL for entries endpoint based on mode
  const buildEntriesUrl = (path) => {
    const params = new URLSearchParams();
    const encodedPath = path ? encodePath(path) : '';
    const pathSuffix = encodedPath ? `/${encodedPath}` : '';

    if (sliceHash) {
      params.set('slice_version.slice_hash', sliceHash);
    }
    const queryString = params.toString();
    return `${apiBaseUrl}/v1/slices/${sliceId}/entries${pathSuffix}${queryString ? `?${queryString}` : ''}`;
  };

  // Build URL for file endpoint based on mode
  const buildFileUrl = (filePath) => {
    const encodedPath = filePath ? encodePath(filePath) : '';
    const pathSuffix = encodedPath ? `/${encodedPath}` : '';
    const params = new URLSearchParams();

    if (sliceHash) {
      params.set('slice_version.slice_hash', sliceHash);
    }
    const queryString = params.toString();
    return `${apiBaseUrl}/v1/slices/${sliceId}/files${pathSuffix}${queryString ? `?${queryString}` : ''}`;
  };

  // Build URL for file history endpoint based on mode
  const buildHistoryUrl = (filePath) => {
    const encodedPath = filePath ? encodePath(filePath) : '';
    const pathSuffix = encodedPath ? `/${encodedPath}` : '';
    return `${apiBaseUrl}/v1/slices/${sliceId}/files/history${pathSuffix}`;
  };

  // Fetch file history from the API
  const fetchFileHistory = async (filePath) => {
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
    } catch (err) {
      setHistoryError('Unable to load file history.');
      setFileHistory([]);
    } finally {
      setHistoryLoading(false);
    }
  };

  // Toggle history panel
  const toggleHistory = () => {
    const newShowHistory = !showHistory;
    setShowHistory(newShowHistory);
    if (newShowHistory && selectedFile && fileHistory.length === 0) {
      fetchFileHistory(selectedFile);
    }
  };

  const canLoad = sliceId !== '';

  useEffect(() => {
    if (!canLoad) {
      return;
    }

    let active = true;
    const controller = new AbortController();

    const loadRoot = async () => {
      setIsLoading(true);
      setError('');

      try {
        const response = await fetchWithAuth(buildEntriesUrl(''), {
          signal: controller.signal,
        });
        if (!response.ok) {
          throw new Error(`Request failed (${response.status})`);
        }
        const payload = await response.json();
        if (!active) {
          return;
        }

        const rootEntries = payload.entries || [];

        // Restore file selection from URL hash if pending
        const pendingFile = pendingFileRef.current;
        if (pendingFile) {
          pendingFileRef.current = null;

          const parts = pendingFile.split('/');
          const allEntries = { '': rootEntries };
          const pathsToExpand = [''];

          // Load parent directory entries along the file path
          for (let i = 1; i < parts.length; i++) {
            if (!active) return;
            const parentPath = parts.slice(0, i).join('/');
            pathsToExpand.push(parentPath);
            try {
              const dirResp = await fetchWithAuth(buildEntriesUrl(parentPath), { signal: controller.signal });
              if (dirResp.ok) {
                const dirData = await dirResp.json();
                allEntries[parentPath] = dirData.entries || [];
              }
            } catch (e) {
              if (e?.name === 'AbortError') return;
              break;
            }
          }

          if (!active) return;
          setTreeEntries(allEntries);
          setExpandedPaths(pathsToExpand);
          setSelectedFile(pendingFile);

          // Load file content
          try {
            const fileResp = await fetchWithAuth(buildFileUrl(pendingFile), { signal: controller.signal });
            if (fileResp.ok && active) {
              const fileData = await fileResp.json();
              setFileContent(decodeBase64(fileData?.file?.content || ''));
            }
          } catch (e) {
            if (active && e?.name !== 'AbortError') {
              setError('Unable to load the file.');
            }
          }
        } else {
          setTreeEntries({ '': rootEntries });
        }
      } catch (err) {
        if (!active || err?.name === 'AbortError') {
          return;
        }
        setError('Unable to load entries. Confirm the file gateway is running and the slice exists.');
      } finally {
        if (active) {
          setIsLoading(false);
        }
      }
    };

    loadRoot();

    return () => {
      active = false;
      controller.abort();
    };
  }, [sliceId, sliceHash]);

  const fetchEntries = async (path) => {
    if (!canLoad) {
      return;
    }

    setIsLoading(true);
    setError('');

    try {
      const response = await fetchWithAuth(buildEntriesUrl(path));
      if (!response.ok) {
        throw new Error(`Request failed (${response.status})`);
      }
      const payload = await response.json();
      setTreeEntries((prev) => ({
        ...prev,
        [path]: payload.entries || [],
      }));
    } catch (err) {
      setError('Unable to load entries. Confirm the file gateway is running and the slice exists.');
    } finally {
      setIsLoading(false);
    }
  };

  const toggleDirectory = async (entry) => {
    const isExpanded = expandedPaths.includes(entry.path);
    if (isExpanded) {
      setExpandedPaths((prev) => prev.filter((path) => path !== entry.path));
      return;
    }

    if (!treeEntries[entry.path]) {
      await fetchEntries(entry.path);
    }

    setExpandedPaths((prev) => [...prev, entry.path]);
  };

  // Push current browser state to navigation history
  const pushBrowserState = useCallback((file) => {
    const params = new URLSearchParams();
    if (file) params.set('file', file);
    if (sliceId) params.set('slice', sliceId);
    if (sliceHash) params.set('sliceHash', sliceHash);
    const qs = params.toString();
    window.history.pushState(null, '', qs ? `#/browser?${qs}` : '#/browser');
  }, [sliceId, sliceHash]);

  useEffect(() => {
    if (sliceId) {
      pushBrowserState(selectedFile || '');
    }
  }, [pushBrowserState, selectedFile, sliceId, sliceHash]);

  const handleEntryClick = async (entry) => {
    const entryKind = normalizeEntryType(entry.type);
    if (entryKind === 'directory') {
      await toggleDirectory(entry);
      return;
    }

    // Close sidebar on mobile after selecting a file
    if (window.innerWidth <= 900) {
      setSidebarOpen(false);
    }

    setSelectedFile(entry.path);
    pushBrowserState(entry.path);
    setFileContent('');
    setIsLoading(true);
    setError('');
    setShowHistory(false);
    setFileHistory([]);
    setHistoryError('');

    try {
      const response = await fetchWithAuth(buildFileUrl(entry.path));
      if (!response.ok) {
        throw new Error(`Request failed (${response.status})`);
      }
      const payload = await response.json();
      const content = payload?.file?.content || '';
      setFileContent(decodeBase64(content));
    } catch (err) {
      setError('Unable to load the file.');
    } finally {
      setIsLoading(false);
    }
  };

  const handleBreadcrumbClick = async (path) => {
    if (path && !treeEntries[path]) {
      await fetchEntries(path);
    }
    if (path) {
      setExpandedPaths((prev) => (prev.includes(path) ? prev : [...prev, path]));
      return;
    }
    setExpandedPaths(['']);
  };

  const renderTree = (path, depth = 0) => {
    const entries = treeEntries[path] || [];
    return (
      <ul className="tree-list">
        {entries.map((entry) => {
          const entryKind = normalizeEntryType(entry.type);
          const isExpanded = expandedPaths.includes(entry.path);
          return (
            <li key={entry.path}>
              <button
                type="button"
                className={`tree-entry ${entryKind}`}
                style={{ paddingLeft: `${depth * 14 + 8}px` }}
                onClick={() => handleEntryClick(entry)}
              >
                <span className="tree-caret">{entryKind === 'directory' ? (isExpanded ? '▼' : '▶') : '•'}</span>
                <span className="entry-icon">{entryKind === 'directory' ? '📁' : '📄'}</span>
                <span className="entry-name">{entry.name}</span>
                {entryKind === 'file' && <span className="entry-meta">{formatBytes(entry.size)}</span>}
              </button>
              {entryKind === 'directory' && isExpanded && renderTree(entry.path, depth + 1)}
            </li>
          );
        })}
      </ul>
    );
  };

  return (
    <section className="repo-browser">
      <div className="repo-main">
        <div className={`repo-layout ${sidebarOpen ? '' : 'sidebar-collapsed'}`}>
          <div
            className={`sidebar-overlay${sidebarOpen ? ' visible' : ''}`}
            onClick={() => setSidebarOpen(false)}
          />
          <aside className={`repo-sidebar ${sidebarOpen ? 'open' : 'closed'}`}>
            <div className="panel-header">
              <h3>File tree</h3>
              <div className="panel-header-actions">
                {isLoading && <span className="status">Loading…</span>}
                <button
                  type="button"
                  className="sidebar-toggle"
                  onClick={() => setSidebarOpen(false)}
                  aria-label="Close sidebar"
                  title="Close sidebar"
                >
                  ✕
                </button>
              </div>
            </div>
            <div className="sidebar-content">
              {error && <div className="panel-error">{error}</div>}
              {!canLoad && <div className="panel-empty">Choose a slice to browse files.</div>}
              {canLoad && !isLoading && !error && (treeEntries[''] || []).length === 0 && (
                <div className="panel-empty">No entries found.</div>
              )}
              {canLoad && renderTree('')}
            </div>
          </aside>

          <div className="repo-code">
            <div className="code-header">
              <div className="code-header-left">
                {!sidebarOpen && (
                  <button
                    type="button"
                    className="sidebar-toggle open-btn"
                    onClick={() => setSidebarOpen(true)}
                    aria-label="Open sidebar"
                    title="Open file tree"
                    data-testid="sidebar-toggle"
                  >
                    ☰
                  </button>
                )}
                <div>
                  <h3>{selectedFile ? selectedFile : 'Select a file'}</h3>
                  <div className="breadcrumbs">
                    {breadcrumbs.map((crumb, index) => (
                      <button
                        key={crumb.path || 'root'}
                        type="button"
                        className="breadcrumb"
                        onClick={() => handleBreadcrumbClick(crumb.path)}
                      >
                        {crumb.name}
                        {index < breadcrumbs.length - 1 && <span className="separator">/</span>}
                      </button>
                    ))}
                  </div>
                </div>
              </div>
              <div className="code-header-actions">
                {selectedFile && <span className="status">{formatBytes(fileContent.length)}</span>}
                {selectedFile && (
                  <button
                    type="button"
                    className={`history-toggle ${showHistory ? 'active' : ''}`}
                    onClick={toggleHistory}
                    data-testid="history-toggle"
                    title={showHistory ? 'Show file content' : 'Show commit history'}
                  >
                    {showHistory ? '📄 Content' : '📜 History'}
                  </button>
                )}
              </div>
            </div>
            <div className="code-content">
              {!selectedFile && <div className="panel-empty">Choose a file from the tree to preview its contents.</div>}
              {selectedFile && !showHistory && (
                <pre className="file-preview">
                  <code dangerouslySetInnerHTML={{ __html: highlightedContent || 'No content available yet.' }} />
                </pre>
              )}
              {selectedFile && showHistory && (
                <div className="history-panel" data-testid="history-panel">
                  {historyLoading && <div className="history-loading">Loading history...</div>}
                  {historyError && <div className="panel-error">{historyError}</div>}
                  {!historyLoading && !historyError && fileHistory.length === 0 && (
                    <div className="panel-empty">No history available for this file.</div>
                  )}
                  {!historyLoading && fileHistory.length > 0 && (
                    <ul className="history-list">
                      {fileHistory.map((change) => (
                        <li key={change.id} className="history-item" data-testid="history-item">
                          <div className="history-item-header">
                            <span className={`change-type change-type-${normalizeChangeType(change.change_type)}`}>
                              {formatChangeType(change.change_type)}
                            </span>
                            <a
                              className="commit-hash commit-diff-link"
                              title={change.commit_hash}
                              href="#"
                              data-testid="commit-diff-link"
                              onClick={(e) => {
                                e.preventDefault();
                                if (change.commit_hash && onNavigateToDiff) {
                                  onNavigateToDiff(change.commit_hash);
                                }
                              }}
                            >
                              {change.commit_hash ? change.commit_hash.slice(0, 7) : 'unknown'}
                            </a>
                          </div>
                          <div className="history-item-message">{change.message || 'No message'}</div>
                          <div className="history-item-meta">
                            <span className="history-author">{change.author || 'Unknown'}</span>
                            <span className="history-date">{formatTimestamp(change.timestamp)}</span>
                            {(change.lines_added > 0 || change.lines_deleted > 0) && (
                              <span className="history-lines">
                                <span className="lines-added">+{change.lines_added || 0}</span>
                                <span className="lines-deleted">-{change.lines_deleted || 0}</span>
                              </span>
                            )}
                          </div>
                        </li>
                      ))}
                    </ul>
                  )}
                </div>
              )}
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}

// ---------------------------------------------------------------------------
// Commit Diff Page Component
// ---------------------------------------------------------------------------

function CommitDiffPage({ commitHash, onBack }) {
  const [diffData, setDiffData] = useState(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!commitHash) return;
    let active = true;
    const controller = new AbortController();

    const loadDiff = async () => {
      setIsLoading(true);
      setError('');
      try {
        const response = await fetchWithAuth(`${apiBaseUrl}/v1/commits/${encodeURIComponent(commitHash)}/changes`, {
          signal: controller.signal,
        });
        if (!response.ok) throw new Error(`Request failed (${response.status})`);
        const payload = await response.json();
        if (active) setDiffData(normalizeDiffResponse(payload));
      } catch (err) {
        if (active && err?.name !== 'AbortError') {
          setError('Unable to load commit changes.');
        }
      } finally {
        if (active) setIsLoading(false);
      }
    };

    loadDiff();
    return () => { active = false; controller.abort(); };
  }, [commitHash]);

  return (
    <section className="commit-diff-page" data-testid="commit-diff-page">
      <div className="diff-header">
        <button type="button" className="ghost diff-back-btn" onClick={onBack} data-testid="diff-back-btn">
          Back to browser
        </button>
        <div>
          <p className="eyebrow">Commit diff</p>
          <h2 data-testid="diff-commit-title">
            Commit <span className="commit-hash">{commitHash ? commitHash.slice(0, 12) : ''}</span>
          </h2>
        </div>
        {diffData && (
          <div className="diff-summary" data-testid="diff-summary">
            <span className="diff-stat diff-stat-added">+{diffData.files_added || 0} added</span>
            <span className="diff-stat diff-stat-modified">{diffData.files_modified || 0} modified</span>
            <span className="diff-stat diff-stat-deleted">-{diffData.files_deleted || 0} deleted</span>
            {(diffData.files_renamed || 0) > 0 && (
              <span className="diff-stat diff-stat-renamed">{diffData.files_renamed} renamed</span>
            )}
          </div>
        )}
      </div>

      <div className="diff-content">
        {isLoading && <div className="diff-loading">Loading commit changes...</div>}
        {error && <div className="panel-error">{error}</div>}
        {!isLoading && !error && diffData && (
          <ul className="diff-file-list" data-testid="diff-file-list">
            {(diffData.changes || []).map((change) => (
              <li key={change.id || change.path} className="diff-file-item" data-testid="diff-file-item">
                <div className="diff-file-header">
                  <span className={`change-type change-type-${normalizeChangeType(change.change_type)}`}>
                    {formatChangeType(change.change_type)}
                  </span>
                  <span className="diff-file-path" data-testid="diff-file-path">{change.path}</span>
                  {change.old_path && change.old_path !== change.path && (
                    <span className="diff-file-old-path">(was: {change.old_path})</span>
                  )}
                </div>
                <div className="diff-file-stats">
                  {(change.lines_added > 0 || change.lines_deleted > 0) && (
                    <span className="history-lines">
                      <span className="lines-added">+{change.lines_added || 0}</span>
                      <span className="lines-deleted">-{change.lines_deleted || 0}</span>
                    </span>
                  )}
                </div>
              </li>
            ))}
          </ul>
        )}
        {!isLoading && !error && diffData && (diffData.changes || []).length === 0 && (
          <div className="panel-empty">No changes found in this commit.</div>
        )}
      </div>
    </section>
  );
}

// ---------------------------------------------------------------------------
// Utility Functions
// ---------------------------------------------------------------------------

function normalizeChange(c) {
  return {
    ...c,
    change_type: c.change_type ?? c.changeType,
    commit_hash: c.commit_hash ?? c.commitHash,
    lines_added: c.lines_added ?? c.linesAdded ?? 0,
    lines_deleted: c.lines_deleted ?? c.linesDeleted ?? 0,
    old_path: c.old_path ?? c.oldPath,
    slice_id: c.slice_id ?? c.sliceId,
  };
}

function normalizeDiffResponse(data) {
  return {
    ...data,
    commit_hash: data.commit_hash ?? data.commitHash,
    files_added: data.files_added ?? data.filesAdded ?? 0,
    files_modified: data.files_modified ?? data.filesModified ?? 0,
    files_deleted: data.files_deleted ?? data.filesDeleted ?? 0,
    files_renamed: data.files_renamed ?? data.filesRenamed ?? 0,
    changes: (data.changes || []).map(normalizeChange),
  };
}

function normalizeSliceInfo(slice) {
  return {
    ...slice,
    slice_id: slice.slice_id ?? slice.sliceId,
    file_count: slice.file_count ?? slice.fileCount,
    is_root: slice.is_root ?? slice.isRoot ?? false,
  };
}

function normalizeChangeType(value) {
  if (value === 1 || value === 'CHANGE_TYPE_ADD' || value === 'ADD') {
    return 'add';
  }
  if (value === 2 || value === 'CHANGE_TYPE_MODIFY' || value === 'MODIFY') {
    return 'modify';
  }
  if (value === 3 || value === 'CHANGE_TYPE_DELETE' || value === 'DELETE') {
    return 'delete';
  }
  if (value === 4 || value === 'CHANGE_TYPE_RENAME' || value === 'RENAME') {
    return 'rename';
  }
  return 'unknown';
}

function formatChangeType(value) {
  const type = normalizeChangeType(value);
  return type.charAt(0).toUpperCase() + type.slice(1);
}

function formatTimestamp(timestamp) {
  if (!timestamp) {
    return 'Unknown date';
  }
  const date = new Date(timestamp * 1000);
  return date.toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

function normalizeEntryType(value) {
  if (value === 2 || value === 'ENTRY_TYPE_DIRECTORY' || value === 'DIRECTORY') {
    return 'directory';
  }
  if (value === 1 || value === 'ENTRY_TYPE_FILE' || value === 'FILE') {
    return 'file';
  }
  return 'file';
}

function decodeBase64(value) {
  if (!value) {
    return '';
  }
  try {
    return decodeURIComponent(escape(window.atob(value)));
  } catch (error) {
    try {
      return window.atob(value);
    } catch (innerError) {
      return value;
    }
  }
}

function formatBytes(value) {
  const numValue = typeof value === 'number' ? value : parseFloat(value);

  if (!numValue || isNaN(numValue)) {
    return '0 B';
  }

  const units = ['B', 'KB', 'MB', 'GB'];
  let size = numValue;
  let unitIndex = 0;
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024;
    unitIndex += 1;
  }
  return `${size.toFixed(size >= 10 || unitIndex === 0 ? 0 : 1)} ${units[unitIndex]}`;
}

function highlightCode(source) {
  if (!source) {
    return '';
  }

  const tokenRegex =
    /\/\/.*|\/\*[\s\S]*?\*\/|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`(?:\\.|[^`\\])*`|\b(?:const|let|var|function|return|if|else|for|while|class|import|from|export|async|await|try|catch|throw|new|switch|case|break|default|true|false|null)\b|\b\d+(?:\.\d+)?\b/g;
  let lastIndex = 0;
  let result = '';

  for (const match of source.matchAll(tokenRegex)) {
    const matchIndex = match.index ?? 0;
    result += escapeHtml(source.slice(lastIndex, matchIndex));
    const token = match[0];
    const className = token.startsWith('//') || token.startsWith('/*')
      ? 'token-comment'
      : token.startsWith('"') || token.startsWith("'") || token.startsWith('`')
      ? 'token-string'
      : /^\d/.test(token)
      ? 'token-number'
      : 'token-keyword';
    result += `<span class="${className}">${escapeHtml(token)}</span>`;
    lastIndex = matchIndex + token.length;
  }

  result += escapeHtml(source.slice(lastIndex));
  return result;
}

function escapeHtml(value) {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

export default App;
