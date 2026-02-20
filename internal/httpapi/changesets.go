package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/auth"
	"github.com/niczy/gitslice/internal/authz"
	"github.com/niczy/gitslice/internal/common"
	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

// ChangesetsAPI exposes HTTP endpoints for changeset workflows used by the web UI.
type ChangesetsAPI struct {
	st storage.Storage
}

func NewChangesetsAPI(st storage.Storage) *ChangesetsAPI {
	return &ChangesetsAPI{st: st}
}

type revertCommitChangeRequest struct {
	ChangeID string `json:"changeId"`
	SliceID  string `json:"sliceId,omitempty"`
	Message  string `json:"message,omitempty"`
}

type revertCommitChangeResponse struct {
	ChangesetID   string `json:"changesetId"`
	ChangesetHash string `json:"changesetHash"`
	SliceID       string `json:"sliceId"`
	Status        string `json:"status"`
	RedirectHash  string `json:"redirectHash"`
}

type changesetInfoResponse struct {
	ChangesetID    string   `json:"changesetId"`
	ChangesetHash  string   `json:"changesetHash"`
	SliceID        string   `json:"sliceId"`
	BaseCommitHash string   `json:"baseCommitHash"`
	ModifiedFiles  []string `json:"modifiedFiles"`
	Status         string   `json:"status"`
	Author         string   `json:"author"`
	Message        string   `json:"message"`
	CreatedAt      int64    `json:"createdAt"`
	MergedAt       int64    `json:"mergedAt"`
}

type changesetDiffSummaryResponse struct {
	FilesAdded    int `json:"filesAdded"`
	FilesModified int `json:"filesModified"`
	FilesDeleted  int `json:"filesDeleted"`
	LinesAdded    int `json:"linesAdded"`
	LinesRemoved  int `json:"linesRemoved"`
}

type changesetDiffEntryResponse struct {
	ID           string `json:"id"`
	SliceID      string `json:"sliceId"`
	Path         string `json:"path"`
	OldPath      string `json:"oldPath,omitempty"`
	ChangeType   string `json:"changeType"`
	LinesAdded   int    `json:"linesAdded"`
	LinesDeleted int    `json:"linesDeleted"`
	Patch        string `json:"patch,omitempty"`
}

type changesetDiffResponse struct {
	Changeset changesetInfoResponse        `json:"changeset"`
	Diff      changesetDiffSummaryResponse `json:"diff"`
	Changes   []changesetDiffEntryResponse `json:"changes"`
}

// HandleCommitChangeRevert handles:
// POST /v1/commits/{commitHash}/changes/revert
func (a *ChangesetsAPI) HandleCommitChangeRevert(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/v1/commits/")
	if path == r.URL.Path || !strings.HasSuffix(path, "/changes/revert") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	commitHash := strings.Trim(strings.TrimSuffix(path, "/changes/revert"), "/")
	if commitHash == "" {
		writeError(w, http.StatusBadRequest, "invalid commit hash")
		return
	}

	username := auth.UsernameFromHTTPRequest(r)
	if username == "" {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	if _, err := a.st.EnsureUser(r.Context(), username); err != nil {
		writeError(w, http.StatusBadRequest, "invalid user")
		return
	}

	var req revertCommitChangeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.ChangeID = strings.TrimSpace(req.ChangeID)
	req.SliceID = strings.TrimSpace(req.SliceID)
	req.Message = strings.TrimSpace(req.Message)
	if req.ChangeID == "" {
		writeError(w, http.StatusBadRequest, "changeId is required")
		return
	}

	changes, err := a.st.GetCommitChanges(r.Context(), commitHash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load commit changes")
		return
	}

	var targetChange *models.FileChangeRecord
	for _, change := range changes {
		if change != nil && change.ID == req.ChangeID {
			targetChange = change
			break
		}
	}
	if targetChange == nil {
		writeError(w, http.StatusNotFound, "change not found")
		return
	}

	targetSliceID := req.SliceID
	if targetSliceID == "" {
		targetSliceID = strings.TrimSpace(targetChange.SliceID)
	}
	if targetSliceID == "" {
		writeError(w, http.StatusBadRequest, "sliceId is required")
		return
	}

	slice, err := a.st.GetSlice(r.Context(), targetSliceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "slice not found")
		return
	}
	if !authz.HasSliceViewAccess(slice, username) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	metadata, err := a.st.GetSliceMetadata(r.Context(), targetSliceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load slice metadata")
		return
	}

	modifiedFiles := modifiedFilesForRevert(targetChange)
	if len(modifiedFiles) == 0 {
		writeError(w, http.StatusBadRequest, "change has no revertable file path")
		return
	}
	for _, fileID := range modifiedFiles {
		if err := common.ValidateFileID(fileID); err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid file path: %s", fileID))
			return
		}
	}

	message := req.Message
	if message == "" {
		message = fmt.Sprintf("Revert %s from %s", targetChange.Path, shortenHash(targetChange.CommitHash))
	}

	now := time.Now()
	cs := &models.Changeset{
		ID:             fmt.Sprintf("cs-%d", now.UnixNano()),
		Hash:           fmt.Sprintf("hash-%d", now.UnixNano()),
		SliceID:        targetSliceID,
		BaseCommitHash: metadata.HeadCommitHash,
		ModifiedFiles:  modifiedFiles,
		Status:         models.ChangesetStatusPending,
		Author:         username,
		Message:        message,
		CreatedAt:      now,
	}
	if err := a.st.CreateChangeset(r.Context(), cs); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create revert changeset")
		return
	}

	writeJSON(w, http.StatusCreated, revertCommitChangeResponse{
		ChangesetID:   cs.ID,
		ChangesetHash: cs.Hash,
		SliceID:       targetSliceID,
		Status:        changesetStatusString(cs.Status),
		RedirectHash:  "#/changesets/" + cs.ID,
	})
}

// HandleChangesetDiff handles:
// GET /v1/changesets/{changesetID}/diff
func (a *ChangesetsAPI) HandleChangesetDiff(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/v1/changesets/")
	if path == r.URL.Path || !strings.HasSuffix(path, "/diff") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	changesetID := strings.Trim(strings.TrimSuffix(path, "/diff"), "/")
	if changesetID == "" {
		writeError(w, http.StatusBadRequest, "invalid changeset id")
		return
	}

	username := auth.UsernameFromHTTPRequest(r)
	if username == "" {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	if _, err := a.st.EnsureUser(r.Context(), username); err != nil {
		writeError(w, http.StatusBadRequest, "invalid user")
		return
	}

	cs, err := a.st.GetChangeset(r.Context(), changesetID)
	if err != nil {
		writeError(w, http.StatusNotFound, "changeset not found")
		return
	}
	slice, err := a.st.GetSlice(r.Context(), cs.SliceID)
	if err != nil {
		writeError(w, http.StatusNotFound, "slice not found")
		return
	}
	if !authz.HasSliceViewAccess(slice, username) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	changes := make([]changesetDiffEntryResponse, 0, len(cs.ModifiedFiles))
	for index, filePath := range cs.ModifiedFiles {
		changes = append(changes, changesetDiffEntryResponse{
			ID:           fmt.Sprintf("%s-%d", cs.ID, index),
			SliceID:      cs.SliceID,
			Path:         filePath,
			ChangeType:   string(models.ChangeTypeModify),
			LinesAdded:   0,
			LinesDeleted: 0,
		})
	}

	mergedAt := int64(0)
	if cs.MergedAt != nil {
		mergedAt = cs.MergedAt.Unix()
	}

	writeJSON(w, http.StatusOK, changesetDiffResponse{
		Changeset: changesetInfoResponse{
			ChangesetID:    cs.ID,
			ChangesetHash:  cs.Hash,
			SliceID:        cs.SliceID,
			BaseCommitHash: cs.BaseCommitHash,
			ModifiedFiles:  append([]string(nil), cs.ModifiedFiles...),
			Status:         changesetStatusString(cs.Status),
			Author:         cs.Author,
			Message:        cs.Message,
			CreatedAt:      cs.CreatedAt.Unix(),
			MergedAt:       mergedAt,
		},
		Diff: changesetDiffSummaryResponse{
			FilesAdded:    0,
			FilesModified: len(changes),
			FilesDeleted:  0,
			LinesAdded:    0,
			LinesRemoved:  0,
		},
		Changes: changes,
	})
}

func modifiedFilesForRevert(change *models.FileChangeRecord) []string {
	if change == nil {
		return nil
	}
	files := make([]string, 0, 2)
	seen := map[string]struct{}{}
	appendFile := func(path string) {
		cleaned := strings.TrimSpace(path)
		if cleaned == "" {
			return
		}
		if _, exists := seen[cleaned]; exists {
			return
		}
		seen[cleaned] = struct{}{}
		files = append(files, cleaned)
	}

	appendFile(change.Path)
	if change.ChangeType == models.ChangeTypeRename {
		appendFile(change.OldPath)
	}
	if len(files) == 0 {
		appendFile(change.OldPath)
	}
	return files
}

func changesetStatusString(status models.ChangesetStatus) string {
	switch status {
	case models.ChangesetStatusApproved:
		return "approved"
	case models.ChangesetStatusRejected:
		return "rejected"
	case models.ChangesetStatusMerged:
		return "merged"
	default:
		return "pending"
	}
}

func shortenHash(hash string) string {
	cleaned := strings.TrimSpace(hash)
	if len(cleaned) > 12 {
		return cleaned[:12]
	}
	return cleaned
}
