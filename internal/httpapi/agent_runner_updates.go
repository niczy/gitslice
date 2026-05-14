package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/authresolver"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	defaultRunnerUpdateWait = 25 * time.Second
	minRunnerUpdateWait     = 1 * time.Second
	maxRunnerUpdateWait     = 30 * time.Second
)

type runnerUpdateWaitResponse struct {
	Changed bool `json:"changed"`
}

func (a *AgentSessionsAPI) HandleRunnerUpdates(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	identity, err := authresolver.RequireHTTPIdentity(r.Context(), a.st, r)
	if err != nil {
		writeAuthResolverError(w, err)
		return
	}
	wait := parseRunnerUpdateWait(r.URL.Query().Get("timeoutMs"))
	updates, unsubscribe := a.svc.SubscribeRunnerUpdates(identity.Username)
	defer unsubscribe()

	changed := false
	timer := time.NewTimer(wait)
	defer timer.Stop()

	select {
	case <-r.Context().Done():
		return
	case _, ok := <-updates:
		changed = ok
	case <-timer.C:
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(runnerUpdateWaitResponse{Changed: changed})
}

func parseRunnerUpdateWait(raw string) time.Duration {
	timeoutMs, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || timeoutMs <= 0 {
		return defaultRunnerUpdateWait
	}
	wait := time.Duration(timeoutMs) * time.Millisecond
	if wait < minRunnerUpdateWait {
		return minRunnerUpdateWait
	}
	if wait > maxRunnerUpdateWait {
		return maxRunnerUpdateWait
	}
	return wait
}

func writeAuthResolverError(w http.ResponseWriter, err error) {
	switch status.Code(err) {
	case codes.Unauthenticated:
		writeError(w, http.StatusUnauthorized, "login required")
	case codes.PermissionDenied:
		writeError(w, http.StatusForbidden, "forbidden")
	default:
		writeError(w, http.StatusInternalServerError, "authentication failed")
	}
}
