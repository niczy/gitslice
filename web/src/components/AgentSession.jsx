import { useEffect, useRef, useState } from 'react';

// ---------------------------------------------------------------------------
// Agent Session Component
// ---------------------------------------------------------------------------

export default function AgentSession({ session, onClose, onMinimize }) {
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
            placeholder={isProcessing ? 'Processing...' : 'Type a command...'}
            autoFocus
            disabled={isProcessing}
          />
        </form>
      </div>
    </div>
  );
}
