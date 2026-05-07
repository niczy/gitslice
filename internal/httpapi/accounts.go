package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/auth"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

type AccountsAPI struct {
	st storage.Storage
}

func NewAccountsAPI(st storage.Storage) *AccountsAPI {
	return &AccountsAPI{st: st}
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, &errorResponse{Error: msg})
}

func (a *AccountsAPI) requireUser(w http.ResponseWriter, r *http.Request) (string, bool) {
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

type loginRequest struct {
	Username string `json:"username"`
}

type meResponse struct {
	User *models.User `json:"user"`
	Now  int64        `json:"now"`
}

// Login is a local/dev helper: it validates/creates the user and expects
// subsequent requests to send `Authorization: User <username>`. Production
// disables that legacy auth shortcut unless explicitly overridden.
func (a *AccountsAPI) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	username := strings.TrimSpace(req.Username)
	if !auth.ValidateUsername(username) {
		writeError(w, http.StatusBadRequest, "invalid username")
		return
	}

	user, err := a.st.EnsureUser(r.Context(), username)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid username")
		return
	}
	writeJSON(w, http.StatusOK, &meResponse{
		User: user,
		Now:  time.Now().Unix(),
	})
}

func (a *AccountsAPI) Me(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	username, ok := a.requireUser(w, r)
	if !ok {
		return
	}

	user, err := a.st.GetUser(r.Context(), username)
	if err != nil {
		user, err = a.st.EnsureUser(r.Context(), username)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid user")
			return
		}
	}
	writeJSON(w, http.StatusOK, &meResponse{
		User: user,
		Now:  time.Now().Unix(),
	})
}
