package agentsession

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/models"
)

const (
	defaultE2BDomain            = "e2b.app"
	defaultE2BAPIURLPattern     = "https://api.%s"
	defaultE2BRuntimeWSPort     = 9000
	defaultE2BRuntimeWSPath     = "/ws"
	defaultE2BRequestTimeout    = 30 * time.Second
	defaultE2BSandboxTimeoutSec = 300
)

type E2BRuntimeProviderConfig struct {
	APIURL         string
	Domain         string
	APIKey         string
	AccessToken    string
	RuntimeWSPort  int
	RuntimeWSPath  string
	RequestTimeout time.Duration
	HTTPClient     *http.Client
}

type e2bRuntimeProvider struct {
	apiURL        string
	domain        string
	apiKey        string
	accessToken   string
	runtimeWSPort int
	runtimeWSPath string
	httpClient    *http.Client
}

func NewE2BRuntimeProvider(cfg E2BRuntimeProviderConfig) RuntimeProvider {
	domain := strings.TrimSpace(cfg.Domain)
	if domain == "" {
		domain = defaultE2BDomain
	}
	apiURL := strings.TrimSpace(cfg.APIURL)
	if apiURL == "" {
		apiURL = fmt.Sprintf(defaultE2BAPIURLPattern, domain)
	}
	runtimeWSPort := cfg.RuntimeWSPort
	if runtimeWSPort <= 0 {
		runtimeWSPort = defaultE2BRuntimeWSPort
	}
	runtimeWSPath := strings.TrimSpace(cfg.RuntimeWSPath)
	if runtimeWSPath == "" {
		runtimeWSPath = defaultE2BRuntimeWSPath
	}
	if !strings.HasPrefix(runtimeWSPath, "/") {
		runtimeWSPath = "/" + runtimeWSPath
	}
	requestTimeout := cfg.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = defaultE2BRequestTimeout
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: requestTimeout}
	}

	return &e2bRuntimeProvider{
		apiURL:        strings.TrimRight(apiURL, "/"),
		domain:        domain,
		apiKey:        strings.TrimSpace(cfg.APIKey),
		accessToken:   strings.TrimSpace(cfg.AccessToken),
		runtimeWSPort: runtimeWSPort,
		runtimeWSPath: runtimeWSPath,
		httpClient:    httpClient,
	}
}

func (p *e2bRuntimeProvider) Start(ctx context.Context, session *models.AgentSession) (*RuntimeStartResult, error) {
	if session == nil {
		return nil, &RuntimeError{Code: "E2B_START_INVALID_SESSION", Message: "session is required"}
	}
	if p.apiKey == "" && p.accessToken == "" {
		return nil, &RuntimeError{Code: "E2B_AUTH_MISSING", Message: "missing E2B credentials"}
	}

	templateID := strings.TrimSpace(session.E2BTemplateID)
	if templateID == "" {
		return nil, &RuntimeError{Code: "E2B_TEMPLATE_MISSING", Message: "missing E2B template id"}
	}

	payload := map[string]any{
		"templateID":            templateID,
		"timeout":               normalizeE2BSandboxTimeout(session.TTLSec),
		"autoPause":             false,
		"secure":                true,
		"allow_internet_access": true,
		"metadata": map[string]string{
			"session_id": strings.TrimSpace(session.SessionID),
			"slice_id":   strings.TrimSpace(session.SliceID),
			"user_id":    strings.TrimSpace(session.UserID),
			"agent_type": strings.TrimSpace(session.AgentType),
		},
		"envVars": map[string]string{
			"GS_SESSION_ID": strings.TrimSpace(session.SessionID),
			"GS_SLICE_ID":   strings.TrimSpace(session.SliceID),
			"GS_USER_ID":    strings.TrimSpace(session.UserID),
			"GS_AGENT_TYPE": strings.TrimSpace(session.AgentType),
		},
	}
	if region := strings.TrimSpace(session.E2BRegion); region != "" {
		payload["region"] = region
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, &RuntimeError{Code: "E2B_START_ENCODE_FAILED", Message: "failed to encode sandbox request", Err: err}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.apiURL+"/sandboxes", bytes.NewReader(body))
	if err != nil {
		return nil, &RuntimeError{Code: "E2B_START_REQUEST_BUILD_FAILED", Message: "failed to build sandbox request", Err: err}
	}
	p.applyAuthHeaders(req)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "gitslice-agent-runtime/1.0")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, &RuntimeError{Code: "E2B_START_REQUEST_FAILED", Message: "sandbox create request failed", Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, &RuntimeError{
			Code:    e2bStartStatusCode(resp.StatusCode),
			Message: readE2BAPIError(resp),
		}
	}

	var response struct {
		SandboxID string `json:"sandboxID"`
		Domain    string `json:"domain"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, &RuntimeError{Code: "E2B_START_DECODE_FAILED", Message: "failed to decode sandbox response", Err: err}
	}

	sandboxID := strings.TrimSpace(response.SandboxID)
	if sandboxID == "" {
		return nil, &RuntimeError{Code: "E2B_START_INVALID_RESPONSE", Message: "sandbox response missing sandboxID"}
	}

	return &RuntimeStartResult{
		Provider:  "e2b",
		SessionID: sandboxID,
		Endpoint:  p.buildRuntimeEndpoint(sandboxID, response.Domain),
		Status:    "ready",
	}, nil
}

func (p *e2bRuntimeProvider) Stop(ctx context.Context, session *models.AgentSession, reason string) error {
	_ = reason
	if session == nil {
		return nil
	}

	sandboxID := strings.TrimSpace(session.RuntimeSessionID)
	if sandboxID == "" {
		sandboxID = strings.TrimSpace(session.E2BSandboxID)
	}
	if sandboxID == "" {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, p.apiURL+"/sandboxes/"+url.PathEscape(sandboxID), nil)
	if err != nil {
		return &RuntimeError{Code: "E2B_STOP_REQUEST_BUILD_FAILED", Message: "failed to build sandbox delete request", Err: err}
	}
	p.applyAuthHeaders(req)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "gitslice-agent-runtime/1.0")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return &RuntimeError{Code: "E2B_STOP_REQUEST_FAILED", Message: "sandbox delete request failed", Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return &RuntimeError{
			Code:    e2bStopStatusCode(resp.StatusCode),
			Message: readE2BAPIError(resp),
		}
	}
	return nil
}

func (p *e2bRuntimeProvider) buildRuntimeEndpoint(sandboxID, sandboxDomain string) string {
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" {
		return ""
	}
	domain := strings.TrimSpace(sandboxDomain)
	if domain == "" {
		domain = p.domain
	}
	if domain == "" {
		domain = defaultE2BDomain
	}
	return fmt.Sprintf("wss://%d-%s.%s%s", p.runtimeWSPort, sandboxID, domain, p.runtimeWSPath)
}

func (p *e2bRuntimeProvider) applyAuthHeaders(req *http.Request) {
	if req == nil {
		return
	}
	if p.apiKey != "" {
		req.Header.Set("X-API-KEY", p.apiKey)
	}
	if p.accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+p.accessToken)
	}
}

func e2bStartStatusCode(statusCode int) string {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "E2B_START_UNAUTHORIZED"
	case http.StatusTooManyRequests:
		return "E2B_START_RATE_LIMITED"
	default:
		if statusCode >= 500 {
			return "E2B_START_UNAVAILABLE"
		}
		return "E2B_START_FAILED"
	}
}

func e2bStopStatusCode(statusCode int) string {
	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "E2B_STOP_UNAUTHORIZED"
	default:
		if statusCode >= 500 {
			return "E2B_STOP_UNAVAILABLE"
		}
		return "E2B_STOP_FAILED"
	}
}

func normalizeE2BSandboxTimeout(ttlSec int) int {
	if ttlSec <= 0 {
		return defaultE2BSandboxTimeoutSec
	}
	return ttlSec
}

func readE2BAPIError(resp *http.Response) string {
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
	if len(message) > 512 {
		message = message[:512]
	}
	return fmt.Sprintf("status=%d body=%s", resp.StatusCode, message)
}
