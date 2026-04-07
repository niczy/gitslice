package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
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
	User          *models.User           `json:"user"`
	Organizations []*models.Organization `json:"organizations"`
	Now           int64                  `json:"now"`
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
	orgs, _ := a.st.ListOrganizationsForUser(r.Context(), username)

	writeJSON(w, http.StatusOK, &meResponse{
		User:          user,
		Organizations: orgs,
		Now:           time.Now().Unix(),
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
	orgs, _ := a.st.ListOrganizationsForUser(r.Context(), username)
	writeJSON(w, http.StatusOK, &meResponse{
		User:          user,
		Organizations: orgs,
		Now:           time.Now().Unix(),
	})
}

type createOrgRequest struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type orgResponse struct {
	Organization *models.Organization `json:"organization"`
}

var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,39}$`)

func slugify(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	// Keep alnum, convert everything else to '-' and collapse repeats.
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 40 {
		out = out[:40]
		out = strings.TrimRight(out, "-")
	}
	return out
}

func (a *AccountsAPI) ListOrgs(w http.ResponseWriter, r *http.Request) {
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
	orgs, err := a.st.ListOrganizationsForUser(r.Context(), username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list orgs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"organizations": orgs})
}

func (a *AccountsAPI) CreateOrg(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	username, ok := a.requireUser(w, r)
	if !ok {
		return
	}

	var req createOrgRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 80 {
		writeError(w, http.StatusBadRequest, "invalid org name")
		return
	}
	slug := strings.TrimSpace(req.Slug)
	if slug == "" {
		slug = slugify(name)
	}
	if !slugRE.MatchString(slug) {
		writeError(w, http.StatusBadRequest, "invalid org slug")
		return
	}

	// Ensure slug uniqueness by appending -N.
	base := slug
	for i := 0; i < 100; i++ {
		if _, err := a.st.GetOrganization(r.Context(), slug); err != nil {
			break
		}
		slug = fmt.Sprintf("%s-%d", base, i+2)
	}

	org := &models.Organization{
		Slug:      slug,
		Name:      name,
		CreatedBy: username,
	}
	if err := a.st.CreateOrganization(r.Context(), org); err != nil {
		writeError(w, http.StatusConflict, "organization already exists")
		return
	}
	_ = a.st.AddOrganizationMember(r.Context(), &models.OrganizationMember{
		OrgSlug:   slug,
		Username:  username,
		Role:      models.OrganizationRoleOwner,
		CreatedAt: time.Now(),
	})

	created, _ := a.st.GetOrganization(r.Context(), slug)
	writeJSON(w, http.StatusOK, &orgResponse{Organization: created})
}
