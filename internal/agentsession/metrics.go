package agentsession

import (
	"expvar"
	"strings"
	"time"
)

var (
	metricAgentSessionCreateTotal        = expvar.NewMap("agent_session_create_total")
	metricAgentSessionStartLatencyMillis = expvar.NewMap("agent_session_start_latency_millis")
	metricAgentSessionRuntimeFailTotal   = expvar.NewMap("agent_session_runtime_fail_total")
	metricAgentWSConnectTotal            = expvar.NewInt("agent_ws_connect_total")
	metricAgentWSReplayGapTotal          = expvar.NewInt("agent_ws_replay_gap_total")
	metricAgentWSBackpressureCloseTotal  = expvar.NewInt("agent_ws_backpressure_close_total")
	metricAgentRuntimeRequestTotal       = expvar.NewMap("agent_runtime_request_total")
	metricAgentRuntimeTokenOutTotal      = expvar.NewMap("agent_runtime_token_out_total")
	metricAgentSessionStartLatencyCount  = expvar.NewMap("agent_session_start_latency_seconds_count")
)

func metricLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer(" ", "_", ",", "_", "=", "_", "|", "_")
	return replacer.Replace(value)
}

func metricKey(parts ...string) string {
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		normalized = append(normalized, metricLabel(part))
	}
	return strings.Join(normalized, "|")
}

func ObserveAgentSessionCreate(agentType, state string) {
	metricAgentSessionCreateTotal.Add(metricKey(agentType, state), 1)
}

func ObserveAgentSessionStartLatency(agentType string, latency time.Duration) {
	millis := latency.Milliseconds()
	if millis < 0 {
		millis = 0
	}
	label := metricLabel(agentType)
	metricAgentSessionStartLatencyMillis.Add(label, millis)
	metricAgentSessionStartLatencyCount.Add(label, 1)
}

func ObserveAgentSessionRuntimeFailure(code string) {
	metricAgentSessionRuntimeFailTotal.Add(metricLabel(code), 1)
}

func ObserveAgentWSConnect() {
	metricAgentWSConnectTotal.Add(1)
}

func ObserveAgentWSReplayGap() {
	metricAgentWSReplayGapTotal.Add(1)
}

func ObserveAgentWSBackpressureClose() {
	metricAgentWSBackpressureCloseTotal.Add(1)
}

func ObserveAgentRuntimeRequest(agentType, outcome string) {
	metricAgentRuntimeRequestTotal.Add(metricKey(agentType, outcome), 1)
}

func ObserveAgentRuntimeTokenOut(agentType string, tokenCount int) {
	if tokenCount <= 0 {
		return
	}
	metricAgentRuntimeTokenOutTotal.Add(metricLabel(agentType), int64(tokenCount))
}

// AgentMetricsSnapshot returns a shallow metrics view for tests/debug tooling.
func AgentMetricsSnapshot() map[string]string {
	return map[string]string{
		"agent_session_create_total":        metricAgentSessionCreateTotal.String(),
		"agent_session_start_latency":       metricAgentSessionStartLatencyMillis.String(),
		"agent_session_runtime_fail_total":  metricAgentSessionRuntimeFailTotal.String(),
		"agent_ws_connect_total":            metricAgentWSConnectTotal.String(),
		"agent_ws_replay_gap_total":         metricAgentWSReplayGapTotal.String(),
		"agent_ws_backpressure_close_total": metricAgentWSBackpressureCloseTotal.String(),
		"agent_runtime_request_total":       metricAgentRuntimeRequestTotal.String(),
		"agent_runtime_token_out_total":     metricAgentRuntimeTokenOutTotal.String(),
		"agent_session_start_latency_count": metricAgentSessionStartLatencyCount.String(),
	}
}
