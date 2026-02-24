import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { mintAgentSessionToken } from '../utils/api.js';

function normalizeWSURL(rawURL = '') {
  if (!rawURL) return '';
  if (rawURL.startsWith('ws://') || rawURL.startsWith('wss://')) {
    return rawURL;
  }
  if (rawURL.startsWith('/')) {
    const scheme = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    return `${scheme}//${window.location.host}${rawURL}`;
  }
  return rawURL;
}

function lineFromFrame(frame) {
  const payload = frame?.payload || {};
  if (frame?.stream === 'agent' && frame?.type === 'output_delta') {
    return { type: 'output', text: payload?.text || '' };
  }
  if (frame?.stream === 'agent' && frame?.type === 'output_final') {
    return { type: 'success', text: payload?.text || '' };
  }
  if (frame?.stream === 'tool' && frame?.type === 'start') {
    return { type: 'output', text: `[tool:start] ${payload?.tool || 'tool'}` };
  }
  if (frame?.stream === 'tool' && frame?.type === 'output') {
    return { type: 'output', text: `[tool] ${payload?.text || ''}` };
  }
  if (frame?.stream === 'tool' && frame?.type === 'end') {
    return { type: 'success', text: `[tool:end] ${payload?.status || 'done'}` };
  }
  if (frame?.stream === 'pty' && frame?.type === 'stdout') {
    return { type: 'output', text: payload?.data || '' };
  }
  if (frame?.stream === 'control' && frame?.type === 'error') {
    return { type: 'error', text: payload?.message || payload?.code || 'runtime error' };
  }
  return null;
}

function toChatMessage(line) {
  const text = String(line?.text || '').trim();
  if (!text) return null;

  if (line?.type === 'prompt') {
    return { role: 'user', text: text.replace(/^\$\s*/, '') };
  }

  if (text.startsWith('[tool')) {
    return { role: 'assistant', text, tone: 'meta' };
  }

  return {
    role: 'assistant',
    text,
    tone: line?.type === 'error' ? 'error' : (line?.type === 'success' ? 'success' : 'default'),
  };
}

export default function AgentSession({
  session,
  sessions = [],
  activeSessionId,
  onSelectSession,
  onClose,
  onCloseSession,
  onMinimize,
  realRuntimeEnabled = false,
  onSessionStateChange,
}) {
  const [isSessionNavOpen, setIsSessionNavOpen] = useState(() => window.innerWidth > 900);
  const [inputValue, setInputValue] = useState('');
  const [lines, setLines] = useState([]);
  const [displayedLines, setDisplayedLines] = useState(0);
  const [isProcessing, setIsProcessing] = useState(false);
  const chatScrollRef = useRef(null);
  const wsRef = useRef(null);
  const lastSeqRef = useRef(0);
  const reconnectTimerRef = useRef(null);

  const appendLine = useCallback((line) => {
    if (!line) return;
    setLines((prev) => [...prev, line]);
  }, []);

  useEffect(() => {
    if (!realRuntimeEnabled && session?.terminalLines) {
      setLines(session.terminalLines);
      setDisplayedLines(0);
      let index = 0;
      const interval = setInterval(() => {
        index += 1;
        setDisplayedLines(index);
        if (index >= session.terminalLines.length) {
          clearInterval(interval);
        }
      }, 50);
      return () => clearInterval(interval);
    }
  }, [realRuntimeEnabled, session?.id, session?.terminalLines]);

  useEffect(() => {
    if (!realRuntimeEnabled || session?.sessionId) {
      return undefined;
    }
    setLines(session?.terminalLines || []);
    setDisplayedLines(0);
    return undefined;
  }, [realRuntimeEnabled, session?.id, session?.sessionId, session?.terminalLines]);

  useEffect(() => {
    if (!realRuntimeEnabled || !session?.sessionId) {
      return undefined;
    }

    let disposed = false;
    setLines(session?.terminalLines?.length > 0 ? session.terminalLines : [{ type: 'output', text: 'Connecting to runtime...' }]);
    setDisplayedLines(0);
    lastSeqRef.current = 0;

    const connect = async (preferSessionWS) => {
      try {
        let wsInfo = preferSessionWS ? session.ws || null : null;
        if (!wsInfo?.url || !wsInfo?.token) {
          wsInfo = await mintAgentSessionToken(session.sessionId);
        }
        if (disposed) return;

        const wsURL = normalizeWSURL(wsInfo.url);
        const joinURL = `${wsURL}${wsURL.includes('?') ? '&' : '?'}token=${encodeURIComponent(wsInfo.token)}&lastSeq=${lastSeqRef.current}`;
        const ws = new WebSocket(joinURL);
        wsRef.current = ws;

        ws.onopen = () => {
          appendLine({ type: 'success', text: 'Runtime connected.' });
          ws.send(JSON.stringify({
            stream: 'control',
            type: 'hello',
            payload: { client: 'web' },
          }));
        };

        ws.onmessage = (event) => {
          try {
            const frame = JSON.parse(event.data);
            if (typeof frame.seq === 'number' && frame.seq > lastSeqRef.current) {
              lastSeqRef.current = frame.seq;
            }
            if (frame?.stream === 'status' && frame?.type === 'state') {
              const nextState = frame?.payload?.state || '';
              if (nextState && onSessionStateChange) {
                onSessionStateChange(session.sessionId || session.id, nextState);
              }
            }
            appendLine(lineFromFrame(frame));
          } catch {
            appendLine({ type: 'error', text: 'invalid runtime frame' });
          }
        };

        ws.onclose = () => {
          const sessionIsTerminal = session?.status === 'stopped' || session?.status === 'failed';
          if (!disposed) {
            appendLine({ type: 'output', text: 'Runtime disconnected.' });
            if (!sessionIsTerminal) {
              reconnectTimerRef.current = setTimeout(() => {
                connect(false);
              }, 500);
            }
          }
        };

        ws.onerror = () => {
          appendLine({ type: 'error', text: 'websocket connection failed' });
        };
      } catch (error) {
        if (!disposed) {
          appendLine({ type: 'error', text: error?.message || 'failed to connect runtime' });
        }
      }
    };

    connect(true);
    return () => {
      disposed = true;
      if (reconnectTimerRef.current) {
        clearTimeout(reconnectTimerRef.current);
        reconnectTimerRef.current = null;
      }
      if (wsRef.current) {
        wsRef.current.close();
        wsRef.current = null;
      }
    };
  }, [appendLine, onSessionStateChange, realRuntimeEnabled, session?.id, session?.sessionId, session?.status, session?.terminalLines, session?.ws]);

  useEffect(() => {
    if (chatScrollRef.current) {
      chatScrollRef.current.scrollTop = chatScrollRef.current.scrollHeight;
    }
  }, [lines, displayedLines, isProcessing]);

  useEffect(() => {
    const handleResize = () => {
      if (window.innerWidth > 900) {
        setIsSessionNavOpen(true);
      }
    };

    window.addEventListener('resize', handleResize);
    return () => window.removeEventListener('resize', handleResize);
  }, []);

  const handleSubmit = async (e) => {
    e.preventDefault();
    if (!inputValue.trim() || isProcessing) return;

    const command = inputValue.trim();
    setInputValue('');
    appendLine({ type: 'prompt', text: command });

    if (realRuntimeEnabled) {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        appendLine({ type: 'error', text: 'runtime is not connected' });
        return;
      }
      ws.send(JSON.stringify({
        stream: 'agent',
        type: 'input',
        payload: { text: command },
      }));
      return;
    }

    setIsProcessing(true);
    await new Promise((r) => setTimeout(r, 400));
    setLines((prev) => [...prev, { type: 'output', text: 'Processing request...' }]);
    await new Promise((r) => setTimeout(r, 500));
    setLines((prev) => [...prev, { type: 'output', text: `Running: ${command}` }]);
    await new Promise((r) => setTimeout(r, 600));

    setLines((prev) => ([
      ...prev,
      { type: 'success', text: 'Done. The request has been completed.' },
      { type: 'output', text: `Processed ${Math.floor(Math.random() * 1000)} items in 0.42s.` },
    ]));
    setIsProcessing(false);
  };

  const visibleLines = realRuntimeEnabled
    ? lines
    : lines.slice(0, Math.max(displayedLines, lines.length - 1));

  const chatMessages = useMemo(() => visibleLines.map(toChatMessage).filter(Boolean), [visibleLines]);

  if (!session) return null;

  return (
    <div className="agent-session-container">
      <div className="agent-session-header">
        <div className="agent-session-title">
          <span className="agent-session-title-icon">🤖</span>
          <span>{session.name}</span>
          {session.sliceName && <span className="agent-session-slice">{session.sliceName}</span>}
          <span className={`agent-status-badge status-${session.status}`}>{session.status}</span>
        </div>
        <div className="agent-session-actions">
          <button type="button" className="agent-session-action-btn" onClick={onMinimize} title="Minimize">−</button>
          <button type="button" className="agent-session-action-btn" onClick={onClose} title="Close">×</button>
        </div>
      </div>

      <div className="agent-session-body">
        <div className="agent-chat-panel">
          <div className="agent-chat-messages" ref={chatScrollRef}>
            {chatMessages.map((message, index) => (
              <div key={`${message.role}-${index}`} className={`chat-row ${message.role === 'user' ? 'chat-row-user' : 'chat-row-assistant'}`}>
                <div className={`chat-bubble chat-${message.role}${message.tone ? ` chat-tone-${message.tone}` : ''}`}>
                  {message.text}
                </div>
              </div>
            ))}
            {isProcessing && (
              <div className="chat-row chat-row-assistant">
                <div className="chat-bubble chat-assistant chat-typing">Agent is thinking…</div>
              </div>
            )}
          </div>
          <form className="agent-input-form" onSubmit={handleSubmit}>
            <input
              type="text"
              className="agent-input"
              value={inputValue}
              onChange={(e) => setInputValue(e.target.value)}
              placeholder={realRuntimeEnabled ? 'Ask the agent anything…' : (isProcessing ? 'Processing…' : 'Send a message…')}
              autoFocus
              disabled={isProcessing}
            />
          </form>
          <button
            type="button"
            className={`session-nav-toggle ${isSessionNavOpen ? 'open' : ''}`}
            onClick={() => setIsSessionNavOpen((value) => !value)}
            aria-label={isSessionNavOpen ? 'Hide session list panel' : 'Show session list panel'}
            title={isSessionNavOpen ? 'Hide sessions' : 'Show sessions'}
          >
            <span className="session-nav-toggle-icon" aria-hidden="true">🗂️</span>
            <span className="session-nav-toggle-label">Sessions</span>
          </button>
        </div>

        <aside className={`agent-session-nav ${isSessionNavOpen ? 'open' : 'closed'}`} aria-label="Session list">
          <div className="agent-session-nav-header">Session List</div>
          <div className="agent-session-nav-items">
            {sessions.length === 0 && <p className="agent-session-nav-empty">No sessions for this slice yet.</p>}
            {sessions.map((sessionItem) => (
              <button
                key={sessionItem.id}
                type="button"
                className={`agent-session-nav-item ${sessionItem.id === activeSessionId ? 'active' : ''}`}
                onClick={() => onSelectSession?.(sessionItem.id)}
              >
                <span className="agent-session-nav-name">{sessionItem.name}</span>
                <span className={`agent-status-badge status-${sessionItem.status}`}>{sessionItem.status}</span>
                <span className="agent-session-nav-meta">{sessionItem.sliceName || 'Slice'}</span>
                <span
                  className="agent-session-nav-close"
                  role="button"
                  tabIndex={0}
                  onClick={(event) => {
                    event.stopPropagation();
                    onCloseSession?.(sessionItem.id, event);
                  }}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter' || event.key === ' ') {
                      event.preventDefault();
                      event.stopPropagation();
                      onCloseSession?.(sessionItem.id, event);
                    }
                  }}
                >
                  ×
                </span>
              </button>
            ))}
          </div>
        </aside>
      </div>
    </div>
  );
}
