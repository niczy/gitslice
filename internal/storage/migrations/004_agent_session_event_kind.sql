ALTER TABLE agent_session_events
  ADD COLUMN IF NOT EXISTS kind text DEFAULT 'event'::text NOT NULL;

UPDATE agent_session_events
SET kind = CASE
  WHEN lower(trim(stream)) = 'control' AND lower(trim(type)) = 'error' THEN 'error'
  WHEN lower(trim(stream)) = 'agent' AND lower(trim(type)) = 'input' THEN 'user_input'
  WHEN lower(trim(stream)) = 'agent' AND lower(trim(type)) IN ('thinking_delta', 'reasoning_delta', 'reasoning_summary_delta') THEN 'thinking'
  WHEN lower(trim(stream)) = 'agent' AND lower(trim(type)) IN ('output_delta', 'output_final') THEN 'model_response'
  WHEN lower(trim(stream)) = 'tool' AND lower(trim(type)) IN ('start', 'call', 'request') THEN 'tool_call'
  WHEN lower(trim(stream)) = 'tool' AND lower(trim(type)) IN ('output', 'result', 'end') THEN 'tool_result'
  WHEN lower(trim(stream)) = 'status' THEN 'status'
  WHEN lower(trim(stream)) = 'control' THEN 'control'
  ELSE 'event'
END
WHERE kind = '' OR kind = 'event';

CREATE INDEX IF NOT EXISTS idx_agent_session_events_kind
  ON agent_session_events (session_id, kind, seq);
