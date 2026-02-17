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
// outside the gRPC-Gateway surface (e.g. updating E2B template).
type SlicesAPI struct {
	st storage.Storage
}

func NewSlicesAPI(st storage.Storage) *SlicesAPI {
	return &SlicesAPI{st: st}
}

type updateSliceE2BTemplateRequest struct {
	E2BTemplateID string `json:"e2bTemplateId"`
}

type updateSliceE2BTemplateResponse struct {
	SliceID       string `json:"sliceId"`
	E2BTemplateID string `json:"e2bTemplateId"`
}

// HandleE2BTemplate handles GET/PUT on /v1/slices/{sliceID}/e2b-template.
func (a *SlicesAPI) HandleE2BTemplate(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Extract sliceID from path: /v1/slices/{sliceID}/e2b-template
	path := strings.TrimPrefix(r.URL.Path, "/v1/slices/")
	path = strings.TrimSuffix(path, "/e2b-template")
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
		writeJSON(w, http.StatusOK, updateSliceE2BTemplateResponse{
			SliceID:       slice.ID,
			E2BTemplateID: slice.E2BTemplateID,
		})

	case http.MethodPut:
		var req updateSliceE2BTemplateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		templateID := strings.TrimSpace(req.E2BTemplateID)

		if err := a.st.UpdateSliceE2BTemplateID(r.Context(), sliceID, templateID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to update e2b template")
			return
		}

		writeJSON(w, http.StatusOK, updateSliceE2BTemplateResponse{
			SliceID:       sliceID,
			E2BTemplateID: templateID,
		})

	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}
