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
)

const (
	defaultE2BDomain        = "e2b.app"
	defaultE2BAPIURLPattern = "https://api.%s"
	defaultRuntimeWSPort    = 9000
	defaultRuntimeWSPath    = "/ws"
	defaultE2BRequestTO     = 30 * time.Second
)

type E2BProviderConfig struct {
	APIURL         string
	Domain         string
	APIKey         string
	AccessToken    string
	RuntimeWSPort  int
	RuntimeWSPath  string
	RequestTimeout time.Duration
	HTTPClient     *http.Client
}

type E2BProvider struct {
	apiURL        string
	domain        string
	apiKey        string
	accessToken   string
	runtimeWSPort int
	runtimeWSPath string
	httpClient    *http.Client
}

func NewE2BProvider(cfg E2BProviderConfig) *E2BProvider {
	domain := strings.TrimSpace(cfg.Domain)
	if domain == "" {
		domain = defaultE2BDomain
	}
	apiURL := strings.TrimSpace(cfg.APIURL)
	if apiURL == "" {
		apiURL = fmt.Sprintf(defaultE2BAPIURLPattern, domain)
	}
	port := cfg.RuntimeWSPort
	if port <= 0 {
		port = defaultRuntimeWSPort
	}
	path := strings.TrimSpace(cfg.RuntimeWSPath)
	if path == "" {
		path = defaultRuntimeWSPath
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	timeout := cfg.RequestTimeout
	if timeout <= 0 {
		timeout = defaultE2BRequestTO
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}

	return &E2BProvider{
		apiURL:        strings.TrimRight(apiURL, "/"),
		domain:        domain,
		apiKey:        strings.TrimSpace(cfg.APIKey),
		accessToken:   strings.TrimSpace(cfg.AccessToken),
		runtimeWSPort: port,
		runtimeWSPath: path,
		httpClient:    httpClient,
	}
}

func (p *E2BProvider) CreateSandbox(ctx context.Context, req RuntimeProvisionRequest) (*RuntimeProvisionResult, error) {
	if strings.TrimSpace(p.apiKey) == "" && strings.TrimSpace(p.accessToken) == "" {
		return nil, errors.New("missing E2B credentials: set E2B_API_KEY or E2B_ACCESS_TOKEN")
	}
	if strings.TrimSpace(req.TemplateID) == "" {
		return nil, errors.New("missing template id")
	}

	body := map[string]any{
		"templateID":            req.TemplateID,
		"timeout":               normalizeTimeoutSeconds(req.TTLSec),
		"autoPause":             false,
		"secure":                true,
		"allow_internet_access": true,
		"metadata":              buildMetadata(req),
		"envVars":               buildEnvVars(req),
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	endpoint := p.apiURL + "/sandboxes"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	p.applyAuthHeaders(httpReq)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", "gitslice-agent-session/1.0")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("create sandbox failed: %s", readAPIError(resp))
	}

	var out struct {
		SandboxID string `json:"sandboxID"`
		Domain    string `json:"domain"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode create sandbox response: %w", err)
	}
	out.SandboxID = strings.TrimSpace(out.SandboxID)
	if out.SandboxID == "" {
		return nil, errors.New("create sandbox response missing sandboxID")
	}
	return &RuntimeProvisionResult{
		SandboxID:       out.SandboxID,
		RuntimeEndpoint: p.buildRuntimeEndpoint(out.SandboxID, out.Domain),
	}, nil
}

func (p *E2BProvider) KillSandbox(ctx context.Context, sandboxID string) error {
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" {
		return nil
	}

	endpoint := p.apiURL + "/sandboxes/" + url.PathEscape(sandboxID)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	p.applyAuthHeaders(httpReq)
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", "gitslice-agent-session/1.0")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("kill sandbox failed: %s", readAPIError(resp))
	}
	return nil
}

func (p *E2BProvider) applyAuthHeaders(req *http.Request) {
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

func (p *E2BProvider) buildRuntimeEndpoint(sandboxID, sandboxDomain string) string {
	sandboxID = strings.TrimSpace(sandboxID)
	if sandboxID == "" {
		return ""
	}
	sandboxDomain = strings.TrimSpace(sandboxDomain)
	if sandboxDomain == "" {
		sandboxDomain = p.domain
	}
	if sandboxDomain == "" {
		sandboxDomain = defaultE2BDomain
	}
	return fmt.Sprintf("wss://%d-%s.%s%s", p.runtimeWSPort, sandboxID, sandboxDomain, p.runtimeWSPath)
}

func buildMetadata(req RuntimeProvisionRequest) map[string]string {
	metadata := map[string]string{
		"session_id": strings.TrimSpace(req.SessionID),
		"slice_id":   strings.TrimSpace(req.SliceID),
		"user_id":    strings.TrimSpace(req.UserID),
	}
	if region := strings.TrimSpace(req.Region); region != "" {
		metadata["region"] = region
	}
	return metadata
}

func buildEnvVars(req RuntimeProvisionRequest) map[string]string {
	out := make(map[string]string, len(req.EnvironmentVars)+4)
	for key, value := range req.EnvironmentVars {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = value
	}
	out["GS_SESSION_ID"] = strings.TrimSpace(req.SessionID)
	out["GS_SLICE_ID"] = strings.TrimSpace(req.SliceID)
	out["GS_USER_ID"] = strings.TrimSpace(req.UserID)
	if region := strings.TrimSpace(req.Region); region != "" {
		out["GS_REGION"] = region
	}
	return out
}

func normalizeTimeoutSeconds(ttlSec int) int {
	if ttlSec <= 0 {
		return 300
	}
	return ttlSec
}

func readAPIError(resp *http.Response) string {
	if resp == nil {
		return "no response"
	}
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	msg := strings.TrimSpace(string(payload))
	if msg != "" {
		var body map[string]any
		if err := json.Unmarshal(payload, &body); err == nil {
			msg = extractErrorMessage(body)
		}
		if len(msg) > 512 {
			msg = msg[:512]
		}
		return fmt.Sprintf("status=%d body=%s", resp.StatusCode, msg)
	}
	return fmt.Sprintf("status=%d", resp.StatusCode)
}

func extractErrorMessage(body map[string]any) string {
	candidates := []string{"message", "error", "detail"}
	for _, key := range candidates {
		if value, ok := body[key]; ok {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(encoded))
}
