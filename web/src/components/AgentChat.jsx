import { useCallback, useEffect, useRef, useState } from 'react';
import { Bot, Plus, Send, Circle } from 'lucide-react';

import { Button } from './ui/button.jsx';

function useAgentSessions(sliceId) {
  const [sessions, setSessions] = useState([]);
  const [loading, setLoading] = useState(false);

  const fetchSessions = useCallback(async () => {
    setLoading(true);
    try {
      const resp = await fetch(`/v1/agent-sessions/slice/${sliceId}`);
      if (resp.ok) {
        const data = await resp.json();
        setSessions(data.sessions || []);
      }
    } catch {
      // ignore
    } finally {
      setLoading(false);
    }
  }, [sliceId]);

  useEffect(() => {
    fetchSessions();
  }, [fetchSessions]);

  return { sessions, loading, refresh: fetchSessions };
}

function relativeTime(ts) {
  if (!ts) return '';
  const d = new Date(ts);
  const now = new Date();
  const minutes = Math.floor((now - d) / 60000);
  if (minutes < 1) return 'just now';
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

function SessionSidebar({ sessions, loading, selectedId, onSelect, onNew, onRefresh }) {
  const activeSessions = sessions.filter(s => s.state === 'running' || s.state === 'starting' || s.state === 'idle');
  const pastSessions = sessions.filter(s => !activeSessions.includes(s));

  return (
    <aside className="agent-sidebar">
      <div className="agent-sidebar-header">
        <div className="agent-sidebar-title">
          <Bot size={16} />
          <span>Agent</span>
        </div>
        <div className="agent-sidebar-actions">
          <Button size="sm" variant="secondary" onClick={onNew} title="Start new session">
            <Plus size={14} />
          </Button>
          <Button size="sm" variant="ghost" onClick={onRefresh} title="Refresh" disabled={loading}>
            <Circle size={12} className={loading ? 'animate-spin' : ''} />
          </Button>
        </div>
      </div>

      <div className="agent-sidebar-body">
        {activeSessions.length === 0 && pastSessions.length === 0 && (
          <div className="agent-sidebar-empty">
            <Bot size={32} className="opacity-30" />
            <p>No agent connected</p>
            <span className="agent-sidebar-status-offline">Start a session to begin</span>
          </div>
        )}

        {activeSessions.length > 0 && (
          <div className="agent-sidebar-section">
            <span className="agent-sidebar-section-label">Active</span>
            {activeSessions.map(s => (
              <button
                key={s.session_id}
                className={`agent-sidebar-item${selectedId === s.session_id ? ' active' : ''}`}
                onClick={() => onSelect(s.session_id)}
              >
                <span className={`agent-status-dot agent-status-${s.state}`} />
                <div className="agent-sidebar-item-text">
                  <span className="agent-sidebar-item-name">{s.agent_type || 'codex'} session</span>
                  <span className="agent-sidebar-item-state">{s.state}</span>
                </div>
              </button>
            ))}
          </div>
        )}

        {pastSessions.length > 0 && (
          <div className="agent-sidebar-section">
            <span className="agent-sidebar-section-label">Past</span>
            {pastSessions.map(s => (
              <button
                key={s.session_id}
                className={`agent-sidebar-item${selectedId === s.session_id ? ' active' : ''}`}
                onClick={() => onSelect(s.session_id)}
              >
                <div className="agent-sidebar-item-text">
                  <span className="agent-sidebar-item-name">{s.agent_type || 'codex'} — {s.state}</span>
                  <span className="agent-sidebar-item-state">{relativeTime(s.updated_at || s.created_at)}</span>
                </div>
              </button>
            ))}
          </div>
        )}
      </div>
    </aside>
  );
}

function ChatArea({ sessionId, sliceId }) {
  const [messages, setMessages] = useState([]);
  const [input, setInput] = useState('');
  const messagesEnd = useRef(null);

  useEffect(() => {
    messagesEnd.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages]);

  const sendMessage = useCallback(async () => {
    if (!input.trim()) return;
    const msg = { role: 'user', text: input };
    setMessages(prev => [...prev, msg]);
    setInput('');
    try {
      await fetch(`/v1/agent-sessions/${sessionId}/input`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ text: input }),
      });
    } catch {
      // ignore
    }
  }, [sessionId, input]);

  const stopSession = useCallback(async () => {
    await fetch(`/v1/agent-sessions/${sessionId}/stop`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ reason: 'user_requested' }),
    });
  }, [sessionId]);

  return (
    <div className="agent-chat-area">
      <div className="agent-chat-header">
        <span className="agent-chat-title">Chat — {sessionId?.slice(0, 12)}...</span>
        <Button variant="outline" size="sm" onClick={stopSession}>Stop</Button>
      </div>
      <div className="agent-chat-messages">
        {messages.length === 0 && (
          <div className="agent-chat-empty">
            <Bot size={40} className="opacity-20" />
            <p>Send a message to start the conversation</p>
          </div>
        )}
        {messages.map((msg, i) => (
          <div key={i} className={`agent-message agent-message-${msg.role}`}>
            <div className="agent-message-bubble">
              {msg.text}
            </div>
          </div>
        ))}
        <div ref={messagesEnd} />
      </div>
      <div className="agent-chat-input">
        <input
          value={input}
          onChange={e => setInput(e.target.value)}
          onKeyDown={e => e.key === 'Enter' && sendMessage()}
          placeholder="Type a message..."
          className="flex-1"
        />
        <Button size="icon" onClick={sendMessage} disabled={!input.trim()}>
          <Send size={16} />
        </Button>
      </div>
    </div>
  );
}

export default function AgentChat({ sliceId, sliceName }) {
  const { sessions, loading, refresh } = useAgentSessions(sliceId);
  const [selectedId, setSelectedId] = useState(null);
  const [isStarting, setIsStarting] = useState(false);

  useEffect(() => {
    const active = sessions.find(s => s.state === 'running' || s.state === 'starting' || s.state === 'idle');
    if (active && !selectedId) {
      setSelectedId(active.session_id);
    }
  }, [sessions, selectedId]);

  const handleNew = useCallback(async () => {
    setIsStarting(true);
    try {
      const resp = await fetch('/v1/agent-sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ slice_id: sliceId, agent_type: 'codex', provider: 'local' }),
      });
      if (resp.ok) {
        const data = await resp.json();
        await refresh();
        setSelectedId(data.sessionId);
      }
    } catch {
      // ignore
    } finally {
      setIsStarting(false);
    }
  }, [sliceId, refresh]);

  return (
    <div className="agent-layout" data-testid="agent-layout">
      <SessionSidebar
        sessions={sessions}
        loading={loading}
        selectedId={selectedId}
        onSelect={setSelectedId}
        onNew={handleNew}
        onRefresh={refresh}
      />
      <div className="agent-main">
        {selectedId ? (
          <ChatArea sessionId={selectedId} sliceId={sliceId} />
        ) : (
          <div className="agent-welcome">
            <Bot size={48} className="opacity-20" />
            <h3>No active agent connected</h3>
            <p>Start a new session to begin chatting with an agent on this slice.</p>
            <Button onClick={handleNew} disabled={isStarting}>
              {isStarting ? 'Starting...' : 'Start Agent Session'}
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}
