package agentsession

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/models"
)

const (
	defaultCFCRequestTimeout = 30 * time.Second
	cfcStartPath             = "/internal/runtime/sessions"
	cfcHealthPath            = "/internal/runtime/health"
)

type CloudflareRuntimeProviderConfig struct {
	ControlBaseURL     string
	ControlAudience    string
	ServiceTokenID     string
	ServiceTokenSecret string
	CodexAPIKey        string
	ClaudeAPIKey       string
	RequestTimeout     time.Duration
	HTTPClient         *http.Client
}

type cloudflareRuntimeProvider struct {
	controlBaseURL     string
	controlAudience    string
	serviceTokenID     string
	serviceTokenSecret string
	codexAPIKey        string
	claudeAPIKey       string
	httpClient         *http.Client
}

func NewCloudflareRuntimeProvider(cfg CloudflareRuntimeProviderConfig) RuntimeProvider {
	requestTimeout := cfg.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = defaultCFCRequestTimeout
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: requestTimeout}
	}

	return &cloudflareRuntimeProvider{
		controlBaseURL:     strings.TrimRight(strings.TrimSpace(cfg.ControlBaseURL), "/"),
		controlAudience:    strings.TrimSpace(cfg.ControlAudience),
		serviceTokenID:     strings.TrimSpace(cfg.ServiceTokenID),
		serviceTokenSecret: strings.TrimSpace(cfg.ServiceTokenSecret),
		codexAPIKey:        strings.TrimSpace(cfg.CodexAPIKey),
		claudeAPIKey:       strings.TrimSpace(cfg.ClaudeAPIKey),
		httpClient:         httpClient,
	}
}

func (p *cloudflareRuntimeProvider) Start(ctx context.Context, session *models.AgentSession) (*RuntimeStartResult, error) {
	if session == nil {
		return nil, &RuntimeError{Code: "CFC_START_INVALID_SESSION", Message: "session is required"}
	}
	if err := p.validateControlConfig(); err != nil {
		return nil, err
	}

	profileID := strings.TrimSpace(session.E2BTemplateID)
	if profileID == "" {
		return nil, &RuntimeError{
			Code:    "CFC_PROFILE_MISSING",
			Message: "missing cloudflare runtime profile id",
		}
	}

	envVars := map[string]string{
		"GS_SESSION_ID": strings.TrimSpace(session.SessionID),
		"GS_SLICE_ID":   strings.TrimSpace(session.SliceID),
		"GS_USER_ID":    strings.TrimSpace(session.UserID),
		"GS_AGENT_TYPE": strings.TrimSpace(session.AgentType),
	}
	credentialName, credentialValue, err := p.agentCredentialForSession(session)
	if err != nil {
		return nil, err
	}
	if credentialName != "" && credentialValue != "" {
		envVars[credentialName] = credentialValue
	}

	payload := map[string]any{
		"sessionId":       strings.TrimSpace(session.SessionID),
		"sliceId":         strings.TrimSpace(session.SliceID),
		"userId":          strings.TrimSpace(session.UserID),
		"agentType":       strings.TrimSpace(session.AgentType),
		"environmentName": strings.TrimSpace(session.EnvironmentName),
		"profileId":       profileID,
		"idleTimeoutSec":  session.IdleTimeoutSec,
		"ttlSec":          session.TTLSec,
		"envVars":         envVars,
	}
	if region := strings.TrimSpace(session.E2BRegion); region != "" {
		payload["region"] = region
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, &RuntimeError{
			Code:    "CFC_START_ENCODE_FAILED",
			Message: "failed to encode start request",
			Err:     err,
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.controlBaseURL+cfcStartPath, bytes.NewReader(body))
	if err != nil {
		return nil, &RuntimeError{
			Code:    "CFC_START_REQUEST_BUILD_FAILED",
			Message: "failed to build start request",
			Err:     err,
		}
	}
	p.applyControlHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "gitslice-agent-runtime/1.0")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		code := "CFC_START_REQUEST_FAILED"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			code = "CFC_START_TIMEOUT"
		}
		return nil, &RuntimeError{
			Code:    code,
			Message: "cloudflare start request failed",
			Err:     err,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusAccepted {
		return nil, &RuntimeError{
			Code:    cfcStartStatusCode(resp.StatusCode),
			Message: p.readControlAPIError(resp),
		}
	}

	var response struct {
		RuntimeSessionID string `json:"runtimeSessionId"`
		SessionID        string `json:"sessionId"`
		ID               string `json:"id"`
		StreamEndpoint   string `json:"streamEndpoint"`
		Endpoint         string `json:"endpoint"`
		RuntimeEndpoint  string `json:"runtimeEndpoint"`
		Status           string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, &RuntimeError{
			Code:    "CFC_START_DECODE_FAILED",
			Message: "failed to decode start response",
			Err:     err,
		}
	}

	runtimeSessionID := firstNonEmpty(response.RuntimeSessionID, response.SessionID, response.ID)
	if runtimeSessionID == "" {
		return nil, &RuntimeError{
			Code:    "CFC_START_INVALID_RESPONSE",
			Message: "start response missing runtime session id",
		}
	}
	status := strings.TrimSpace(response.Status)
	if status == "" {
		status = "ready"
	}

	return &RuntimeStartResult{
		Provider:  RuntimeProviderCloudflareContainers,
		SessionID: runtimeSessionID,
		Endpoint:  firstNonEmpty(response.StreamEndpoint, response.Endpoint, response.RuntimeEndpoint),
		Status:    status,
	}, nil
}

func (p *cloudflareRuntimeProvider) Stop(ctx context.Context, session *models.AgentSession, reason string) error {
	_ = reason
	if session == nil {
		return nil
	}
	if err := p.validateControlConfig(); err != nil {
		return err
	}

	runtimeSessionID := strings.TrimSpace(session.RuntimeSessionID)
	if runtimeSessionID == "" {
		runtimeSessionID = strings.TrimSpace(session.E2BSandboxID)
	}
	if runtimeSessionID == "" {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, p.controlBaseURL+cfcStartPath+"/"+url.PathEscape(runtimeSessionID), nil)
	if err != nil {
		return &RuntimeError{
			Code:    "CFC_STOP_REQUEST_BUILD_FAILED",
			Message: "failed to build stop request",
			Err:     err,
		}
	}
	p.applyControlHeaders(req)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "gitslice-agent-runtime/1.0")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		code := "CFC_STOP_REQUEST_FAILED"
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			code = "CFC_STOP_TIMEOUT"
		}
		return &RuntimeError{
			Code:    code,
			Message: "cloudflare stop request failed",
			Err:     err,
		}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusAccepted {
		return &RuntimeError{
			Code:    cfcStopStatusCode(resp.StatusCode),
			Message: p.readControlAPIError(resp),
		}
	}
	return nil
}

func (p *cloudflareRuntimeProvider) HealthCheck(ctx context.Context) error {
	if err := p.validateControlConfig(); err != nil {
		return err
	}
	if p.codexAPIKey == "" && p.claudeAPIKey == "" {
		return &RuntimeError{
			Code:    "AGENT_CREDENTIAL_MISSING",
			Message: "no runtime model credentials configured",
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.controlBaseURL+cfcHealthPath, nil)
	if err != nil {
		return &RuntimeError{
			Code:    "CFC_HEALTH_REQUEST_BUILD_FAILED",
			Message: "failed to build health check request",
			Err:     err,
		}
	}
	p.applyControlHeaders(req)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "gitslice-agent-runtime/1.0")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return &RuntimeError{
			Code:    "CFC_RUNTIME_UNAVAILABLE",
			Message: "cloudflare runtime control plane unreachable",
			Err:     err,
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &RuntimeError{
			Code:    "CFC_RUNTIME_UNAVAILABLE",
			Message: p.readControlAPIError(resp),
		}
	}
	return nil
}

func (p *cloudflareRuntimeProvider) validateControlConfig() error {
	if p.controlBaseURL == "" {
		return &RuntimeError{
			Code:    "CFC_CONTROL_URL_MISSING",
			Message: "missing cloudflare control base url",
		}
	}
	if p.serviceTokenID == "" || p.serviceTokenSecret == "" {
		return &RuntimeError{
			Code:    "CFC_AUTH_MISSING",
			Message: "missing Cloudflare service token credentials",
		}
	}
	return nil
}

func (p *cloudflareRuntimeProvider) applyControlHeaders(req *http.Request) {
	if req == nil {
		return
	}
	req.Header.Set("CF-Access-Client-Id", p.serviceTokenID)
	req.Header.Set("CF-Access-Client-Secret", p.serviceTokenSecret)
	if p.controlAudience != "" {
		req.Header.Set("X-Gitslice-Audience", p.controlAudience)
	}
}

func (p *cloudflareRuntimeProvider) agentCredentialForSession(session *models.AgentSession) (string, string, error) {
	if session == nil {
		return "", "", &RuntimeError{Code: "AGENT_CREDENTIAL_MISSING", Message: "session is required"}
	}
	switch strings.TrimSpace(session.AgentType) {
	case "codex":
		if p.codexAPIKey == "" {
			return "", "", &RuntimeError{
				Code:    "AGENT_CREDENTIAL_MISSING",
				Message: "missing credential for codex runtime",
			}
		}
		return "OPENAI_API_KEY", p.codexAPIKey, nil
	case "claude":
		if p.claudeAPIKey == "" {
			return "", "", &RuntimeError{
				Code:    "AGENT_CREDENTIAL_MISSING",
				Message: "missing credential for claude runtime",
			}
		}
		return "ANTHROPIC_API_KEY", p.claudeAPIKey, nil
	default:
		return "", "", nil
	}
}

func cfcStartStatusCode(statusCode int) string {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "CFC_START_UNAUTHORIZED"
	case http.StatusTooManyRequests:
		return "CFC_START_RATE_LIMITED"
	default:
		if statusCode >= 500 {
			return "CFC_RUNTIME_UNAVAILABLE"
		}
		return "CFC_START_FAILED"
	}
}

func cfcStopStatusCode(statusCode int) string {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "CFC_STOP_UNAUTHORIZED"
	default:
		if statusCode >= 500 {
			return "CFC_RUNTIME_UNAVAILABLE"
		}
		return "CFC_STOP_FAILED"
	}
}

func (p *cloudflareRuntimeProvider) readControlAPIError(resp *http.Response) string {
	if resp == nil {
		return "no response"
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if len(body) == 0 {
		return fmt.Sprintf("status=%d", resp.StatusCode)
	}

	message := strings.TrimSpace(string(body))
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err == nil {
		for _, key := range []string{"message", "error", "detail"} {
			if value, ok := parsed[key]; ok {
				if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
					message = strings.TrimSpace(text)
					break
				}
			}
		}
	}
	message = redactSecrets(message, p.serviceTokenID, p.serviceTokenSecret, p.codexAPIKey, p.claudeAPIKey)
	if len(message) > 512 {
		message = message[:512]
	}
	return fmt.Sprintf("status=%d body=%s", resp.StatusCode, message)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}
