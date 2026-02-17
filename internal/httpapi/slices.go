package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/niczy/gitslice/internal/auth"
	"github.com/niczy/gitslice/internal/authz"
	"github.com/niczy/gitslice/internal/storage"
)

// SlicesAPI handles HTTP endpoints for slice configuration that sit
// outside the gRPC-Gateway surface (e.g. updating the environment).
type SlicesAPI struct {
	st storage.Storage
}

func NewSlicesAPI(st storage.Storage) *SlicesAPI {
	return &SlicesAPI{st: st}
}

type updateSliceEnvironmentRequest struct {
	Environment string `json:"environment"`
}

type updateSliceEnvironmentResponse struct {
	SliceID     string `json:"sliceId"`
	Environment string `json:"environment"`
}

// HandleEnvironment handles GET/PUT on /v1/slices/{sliceID}/environment.
func (a *SlicesAPI) HandleEnvironment(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Extract sliceID from path: /v1/slices/{sliceID}/environment
	path := strings.TrimPrefix(r.URL.Path, "/v1/slices/")
	path = strings.TrimSuffix(path, "/environment")
	sliceID := strings.Trim(path, "/")
	if sliceID == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	username := auth.UsernameFromHTTPRequest(r)
	if username == "" {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}

	slice, err := a.st.GetSlice(r.Context(), sliceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "slice not found")
		return
	}
	if !authz.HasSliceViewAccess(slice, username) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, updateSliceEnvironmentResponse{
			SliceID:     slice.ID,
			Environment: slice.Environment,
		})

	case http.MethodPut:
		var req updateSliceEnvironmentRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		environment := strings.TrimSpace(req.Environment)

		if err := a.st.UpdateSliceEnvironment(r.Context(), sliceID, environment); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update environment")
			return
		}

		writeJSON(w, http.StatusOK, updateSliceEnvironmentResponse{
			SliceID:     sliceID,
			Environment: environment,
		})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
