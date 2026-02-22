import { useCallback, useEffect, useRef, useState } from 'react';
import { mintAgentSessionToken } from '../utils/api.js';

// ---------------------------------------------------------------------------
// Agent Session Component
// ---------------------------------------------------------------------------

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
    return { type: 'output', text: `[error] ${payload?.message || payload?.code || 'runtime error'}` };
  }
  return null;
}

export default function AgentSession({
  session,
  onClose,
  onMinimize,
  realRuntimeEnabled = false,
  onSessionStateChange,
}) {
  const [inputValue, setInputValue] = useState('');
  const [lines, setLines] = useState([]);
  const [displayedLines, setDisplayedLines] = useState(0);
  const [isProcessing, setIsProcessing] = useState(false);
  const terminalRef = useRef(null);
  const wsRef = useRef(null);
  const lastSeqRef = useRef(0);
  const reconnectTimerRef = useRef(null);

  const appendLine = useCallback((line) => {
    if (!line) return;
    setLines((prev) => [...prev, line]);
  }, []);

  // Mock-mode staggered loading animation
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
            appendLine({ type: 'output', text: '[error] invalid runtime frame' });
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
          appendLine({ type: 'output', text: '[error] websocket connection failed' });
        };
      } catch (error) {
        if (!disposed) {
          appendLine({ type: 'output', text: `[error] ${error?.message || 'failed to connect runtime'}` });
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
    if (terminalRef.current) {
      terminalRef.current.scrollTop = terminalRef.current.scrollHeight;
    }
  }, [lines, displayedLines]);

  const handleSubmit = async (e) => {
    e.preventDefault();
    if (!inputValue.trim() || isProcessing) return;

    const command = inputValue.trim();
    if (!command) return;

    setInputValue('');
    appendLine({ type: 'prompt', text: `$ ${command}` });

    if (realRuntimeEnabled) {
      const ws = wsRef.current;
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        appendLine({ type: 'output', text: '[error] runtime is not connected' });
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
    setLines((prev) => [...prev, { type: 'output', text: 'Processing command...' }]);

    await new Promise((r) => setTimeout(r, 500));
    setLines((prev) => [...prev, { type: 'output', text: `Executing: ${command}` }]);

    await new Promise((r) => setTimeout(r, 600));

    const results = [
      { type: 'success', text: '✓ Command executed successfully' },
      { type: 'output', text: `Output: ${Math.floor(Math.random() * 1000)} items processed` },
      { type: 'output', text: 'Time: 0.42s' },
    ];

    setLines((prev) => [...prev, ...results, { type: 'prompt', text: '$ _' }]);
    setIsProcessing(false);
  };

  if (!session) return null;

  const visibleLines = realRuntimeEnabled
    ? lines
    : lines.slice(0, Math.max(displayedLines, lines.length - 1));

  return (
    <div className="agent-session-container">
      <div className="agent-session-header">
        <div className="agent-session-title">
          <span className="agent-session-title-icon">🤖</span>
          <span>{session.name}</span>
          {session.sliceName && <span className="agent-session-slice">{session.sliceName}</span>}
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
            placeholder={realRuntimeEnabled ? 'Send prompt to runtime...' : (isProcessing ? 'Processing...' : 'Type a command...')}
            autoFocus
            disabled={isProcessing}
          />
        </form>
      </div>
    </div>
  );
}
