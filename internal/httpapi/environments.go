package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/niczy/gitslice/internal/auth"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

type EnvironmentsAPI struct {
	st storage.Storage
}

func NewEnvironmentsAPI(st storage.Storage) *EnvironmentsAPI {
	return &EnvironmentsAPI{st: st}
}

type environmentRequest struct {
	Name           string            `json:"name"`
	DisplayName    string            `json:"displayName"`
	Provider       string            `json:"provider"`
	ProviderID     string            `json:"providerId"`
	ProviderConfig map[string]string `json:"providerConfig"`
	Region         string            `json:"region"`
}

type environmentResponse struct {
	Name           string            `json:"name"`
	DisplayName    string            `json:"displayName"`
	Region         string            `json:"region"`
	CreatedAt      string            `json:"createdAt"`
	UpdatedAt      string            `json:"updatedAt"`
	Provider       string            `json:"provider,omitempty"`
	ProviderID     string            `json:"providerId,omitempty"`
	ProviderConfig map[string]string `json:"providerConfig,omitempty"`
}

type listEnvironmentsResponse struct {
	Environments []environmentResponse `json:"environments"`
}

func (a *EnvironmentsAPI) requireUser(w http.ResponseWriter, r *http.Request) (string, bool) {
	username := auth.UsernameFromHTTPRequest(r)
	if username == "" {
		writeError(w, http.StatusUnauthorized, "login required")
		return "", false
	}
	if _, err := a.st.EnsureUser(r.Context(), username); err != nil {
		writeError(w, http.StatusBadRequest, "invalid user")
		return "", false
	}
	return username, true
}

func includeEnvironmentAdminFields(r *http.Request) bool {
	return r != nil && strings.TrimSpace(r.Header.Get("X-Internal-Caller")) == "1"
}

func mapEnvironmentResponse(env *models.Environment, includeAdmin bool) environmentResponse {
	resp := environmentResponse{
		Name:        env.Name,
		DisplayName: env.DisplayName,
		Region:      env.Region,
		CreatedAt:   env.CreatedAt.Format(timeRFC3339Micro),
		UpdatedAt:   env.UpdatedAt.Format(timeRFC3339Micro),
	}
	if includeAdmin {
		resp.Provider = env.Provider
		resp.ProviderID = env.ProviderID
		resp.ProviderConfig = env.ProviderConfig
	}
	return resp
}

func (a *EnvironmentsAPI) HandleCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.listEnvironments(w, r)
	case http.MethodPost:
		a.createEnvironment(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *EnvironmentsAPI) HandleItem(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/v1/environments/")
	name := strings.TrimSpace(strings.Trim(path, "/"))
	if name == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	switch r.Method {
	case http.MethodGet:
		a.getEnvironment(w, r, name)
	case http.MethodPut:
		a.updateEnvironment(w, r, name)
	case http.MethodDelete:
		a.deleteEnvironment(w, r, name)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *EnvironmentsAPI) listEnvironments(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireUser(w, r); !ok {
		return
	}

	limit := 200
	offset := 0
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = parsed
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("offset")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "invalid offset")
			return
		}
		offset = parsed
	}

	envs, err := a.st.ListEnvironments(r.Context(), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list environments")
		return
	}

	includeAdmin := includeEnvironmentAdminFields(r)
	out := make([]environmentResponse, 0, len(envs))
	for _, env := range envs {
		out = append(out, mapEnvironmentResponse(env, includeAdmin))
	}
	writeJSON(w, http.StatusOK, listEnvironmentsResponse{Environments: out})
}

func (a *EnvironmentsAPI) createEnvironment(w http.ResponseWriter, r *http.Request) {
	username, ok := a.requireUser(w, r)
	if !ok {
		return
	}

	var req environmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	name := strings.TrimSpace(req.Name)
	if err := storage.ValidateEnvironmentProviderConfig(req.Provider, req.ProviderConfig); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	env := &models.Environment{
		Name:           name,
		DisplayName:    req.DisplayName,
		Provider:       req.Provider,
		ProviderID:     req.ProviderID,
		ProviderConfig: req.ProviderConfig,
		Region:         req.Region,
		CreatedBy:      username,
	}
	if err := a.st.CreateEnvironment(r.Context(), env); err != nil {
		switch err {
		case storage.ErrInvalidInput:
			writeError(w, http.StatusBadRequest, "invalid environment")
		case storage.ErrEntryExists:
			writeError(w, http.StatusConflict, "environment already exists")
		default:
			writeError(w, http.StatusInternalServerError, "failed to create environment")
		}
		return
	}

	created, err := a.st.GetEnvironment(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load environment")
		return
	}
	writeJSON(w, http.StatusCreated, mapEnvironmentResponse(created, includeEnvironmentAdminFields(r)))
}

func (a *EnvironmentsAPI) getEnvironment(w http.ResponseWriter, r *http.Request, name string) {
	if _, ok := a.requireUser(w, r); !ok {
		return
	}

	env, err := a.st.GetEnvironment(r.Context(), name)
	if err != nil {
		switch err {
		case storage.ErrInvalidInput:
			writeError(w, http.StatusBadRequest, "invalid environment name")
		case storage.ErrEntryNotFound:
			writeError(w, http.StatusNotFound, "environment not found")
		default:
			writeError(w, http.StatusInternalServerError, "failed to load environment")
		}
		return
	}
	writeJSON(w, http.StatusOK, mapEnvironmentResponse(env, includeEnvironmentAdminFields(r)))
}

func (a *EnvironmentsAPI) updateEnvironment(w http.ResponseWriter, r *http.Request, name string) {
	if _, ok := a.requireUser(w, r); !ok {
		return
	}

	var req environmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	current, err := a.st.GetEnvironment(r.Context(), name)
	if err != nil {
		switch err {
		case storage.ErrInvalidInput:
			writeError(w, http.StatusBadRequest, "invalid environment name")
		case storage.ErrEntryNotFound:
			writeError(w, http.StatusNotFound, "environment not found")
		default:
			writeError(w, http.StatusInternalServerError, "failed to load environment")
		}
		return
	}

	if err := storage.ValidateEnvironmentProviderConfig(req.Provider, req.ProviderConfig); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	current.DisplayName = req.DisplayName
	current.Provider = req.Provider
	current.ProviderID = req.ProviderID
	current.ProviderConfig = req.ProviderConfig
	current.Region = req.Region
	if err := a.st.UpdateEnvironment(r.Context(), current); err != nil {
		switch err {
		case storage.ErrInvalidInput:
			writeError(w, http.StatusBadRequest, "invalid environment")
		case storage.ErrEntryNotFound:
			writeError(w, http.StatusNotFound, "environment not found")
		default:
			writeError(w, http.StatusInternalServerError, "failed to update environment")
		}
		return
	}

	updated, err := a.st.GetEnvironment(r.Context(), name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load environment")
		return
	}
	writeJSON(w, http.StatusOK, mapEnvironmentResponse(updated, includeEnvironmentAdminFields(r)))
}

func (a *EnvironmentsAPI) deleteEnvironment(w http.ResponseWriter, r *http.Request, name string) {
	if _, ok := a.requireUser(w, r); !ok {
		return
	}

	if err := a.st.DeleteEnvironment(r.Context(), name); err != nil {
		switch err {
		case storage.ErrInvalidInput:
			writeError(w, http.StatusBadRequest, "invalid environment name")
		case storage.ErrEntryNotFound:
			writeError(w, http.StatusNotFound, "environment not found")
		default:
			writeError(w, http.StatusInternalServerError, "failed to delete environment")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
