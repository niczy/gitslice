package storage

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/niczy/gitslice/internal/models"
)

type sliceFileState struct {
	exists        bool
	hash          string
	executable    bool
	symlinkTarget string
}

type sliceFilePayload struct {
	state        sliceFileState
	content      []byte
	contentKnown bool
}

func ListDivergentConflicts(ctx context.Context, st Storage) ([]*models.FileConflict, error) {
	candidates, err := st.ListConflicts(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]*models.FileConflict, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == nil {
			continue
		}
		sliceIDs := normalizeConflictSliceIDs(candidate.ConflictingSlices)
		if len(sliceIDs) < 2 {
			continue
		}
		differing, err := DivergentSlicesForPreferred(ctx, st, candidate.FileID, sliceIDs[0], sliceIDs[1:])
		if err != nil {
			return nil, err
		}
		if len(differing) == 0 {
			continue
		}
		out = append(out, &models.FileConflict{
			FileID:            candidate.FileID,
			ConflictingSlices: sliceIDs,
		})
	}
	return out, nil
}

func DivergentSlicesForPreferred(ctx context.Context, st Storage, fileID, preferredSliceID string, candidateSliceIDs []string) ([]string, error) {
	preferredState, err := loadSliceFileState(ctx, st, preferredSliceID, fileID)
	if err != nil {
		return nil, err
	}

	normalizedCandidates := normalizeConflictSliceIDs(candidateSliceIDs)
	out := make([]string, 0, len(normalizedCandidates))
	for _, sliceID := range normalizedCandidates {
		if sliceID == preferredSliceID {
			continue
		}
		currentState, err := loadSliceFileState(ctx, st, sliceID, fileID)
		if err != nil {
			return nil, err
		}
		if currentState != preferredState {
			out = append(out, sliceID)
		}
	}
	return out, nil
}

func NormalizeConflictToPreferred(ctx context.Context, st Storage, fileID, preferredSliceID string) (*models.FileConflict, error) {
	fileID = strings.TrimSpace(fileID)
	preferredSliceID = strings.TrimSpace(preferredSliceID)
	if fileID == "" || preferredSliceID == "" {
		return nil, ErrInvalidInput
	}

	activeSlices, err := st.GetActiveSlicesForFile(ctx, fileID)
	if err != nil {
		return nil, err
	}
	sliceIDs := normalizeConflictSliceIDs(activeSlices)
	foundPreferred := false
	for _, sliceID := range sliceIDs {
		if sliceID == preferredSliceID {
			foundPreferred = true
			break
		}
	}
	if !foundPreferred {
		return nil, ErrInvalidInput
	}

	preferredPayload, err := loadSliceFilePayload(ctx, st, preferredSliceID, fileID)
	if err != nil {
		return nil, err
	}
	if !preferredPayload.state.exists || !preferredPayload.contentKnown {
		return st.ResolveConflict(ctx, fileID, preferredSliceID)
	}

	for _, sliceID := range sliceIDs {
		if sliceID == preferredSliceID {
			continue
		}
		if err := writeSliceFilePayload(ctx, st, sliceID, fileID, preferredPayload); err != nil {
			return nil, err
		}
	}

	return &models.FileConflict{FileID: fileID, ConflictingSlices: []string{}}, nil
}

func normalizeConflictSliceIDs(sliceIDs []string) []string {
	if len(sliceIDs) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(sliceIDs))
	out := make([]string, 0, len(sliceIDs))
	for _, raw := range sliceIDs {
		sliceID := strings.TrimSpace(raw)
		if sliceID == "" {
			continue
		}
		if _, ok := seen[sliceID]; ok {
			continue
		}
		seen[sliceID] = struct{}{}
		out = append(out, sliceID)
	}
	sort.Strings(out)
	return out
}

func loadSliceFileState(ctx context.Context, st Storage, sliceID, fileID string) (sliceFileState, error) {
	sliceID = strings.TrimSpace(sliceID)
	fileID = strings.TrimSpace(fileID)

	slice, err := st.GetSlice(ctx, sliceID)
	if err != nil && err != ErrSliceNotFound {
		return sliceFileState{}, err
	}

	entry, err := st.GetEntryByPath(ctx, strings.TrimSpace(sliceID), strings.TrimSpace(fileID))
	if err != nil {
		if err == ErrEntryNotFound {
			return loadSliceFileStateFromParent(ctx, st, slice, fileID)
		}
		return sliceFileState{}, err
	}
	if entry == nil || entry.Type != "file" {
		return loadSliceFileStateFromParent(ctx, st, slice, fileID)
	}

	state := sliceFileState{
		exists:        true,
		hash:          strings.TrimSpace(entry.Hash),
		executable:    entry.Executable,
		symlinkTarget: strings.TrimSpace(entry.SymlinkTarget),
	}

	manifest, err := st.GetFileManifest(ctx, strings.TrimSpace(sliceID), strings.TrimSpace(fileID))
	if err != nil {
		if err == ErrEntryNotFound {
			return mergeParentState(ctx, st, slice, fileID, state)
		}
		return sliceFileState{}, err
	}
	if manifest != nil {
		if hash := strings.TrimSpace(manifest.Hash); hash != "" {
			state.hash = hash
		}
		state.executable = manifest.Executable
		state.symlinkTarget = strings.TrimSpace(manifest.SymlinkTarget)
	}

	return mergeParentState(ctx, st, slice, fileID, state)
}

func loadSliceFilePayload(ctx context.Context, st Storage, sliceID, fileID string) (sliceFilePayload, error) {
	sliceID = strings.TrimSpace(sliceID)
	fileID = strings.TrimSpace(fileID)

	slice, err := st.GetSlice(ctx, sliceID)
	if err != nil && err != ErrSliceNotFound {
		return sliceFilePayload{}, err
	}

	state, err := loadSliceFileState(ctx, st, sliceID, fileID)
	if err != nil {
		return sliceFilePayload{}, err
	}
	if !state.exists {
		return sliceFilePayload{state: state}, nil
	}

	content, err := ReadSliceFileContent(ctx, st, sliceID, fileID)
	if err == nil {
		return sliceFilePayload{
			state:        state,
			content:      append([]byte(nil), content.Content...),
			contentKnown: true,
		}, nil
	}
	if err != ErrEntryNotFound {
		return sliceFilePayload{}, err
	}

	if hash := strings.TrimSpace(state.hash); hash != "" {
		content, err = ReadVersionedFileContent(ctx, st, hash)
		if err == nil {
			return sliceFilePayload{
				state:        state,
				content:      append([]byte(nil), content.Content...),
				contentKnown: true,
			}, nil
		}
		if err != ErrEntryNotFound {
			return sliceFilePayload{}, err
		}
	}

	if slice != nil && strings.TrimSpace(slice.ParentSlice) != "" && slice.ParentSlice != sliceID {
		content, err = ReadSliceFileContent(ctx, st, slice.ParentSlice, fileID)
		if err == nil {
			return sliceFilePayload{
				state:        state,
				content:      append([]byte(nil), content.Content...),
				contentKnown: true,
			}, nil
		}
		if err != ErrEntryNotFound {
			return sliceFilePayload{}, err
		}
	}

	return sliceFilePayload{state: state}, nil
}

func writeSliceFilePayload(ctx context.Context, st Storage, sliceID, fileID string, payload sliceFilePayload) error {
	if !payload.state.exists {
		return fmt.Errorf("cannot write missing preferred state for %s", fileID)
	}

	existing, err := st.GetEntryByPath(ctx, sliceID, fileID)
	if err != nil && err != ErrEntryNotFound {
		return err
	}

	manifest, err := WriteSliceFileManifestWithMetadata(ctx, st, sliceID, fileID, payload.content, payload.state.executable, payload.state.symlinkTarget)
	if err != nil {
		return err
	}

	entry := &models.DirectoryEntry{
		ID:            fmt.Sprintf("%s:%s", sliceID, fileID),
		Path:          fileID,
		Type:          "file",
		ParentID:      sliceID,
		Size:          manifest.TotalSize,
		Hash:          manifest.Hash,
		Executable:    manifest.Executable,
		SymlinkTarget: manifest.SymlinkTarget,
	}
	if existing != nil {
		entry.ID = existing.ID
		entry.ParentID = existing.ParentID
		if entry.ParentID == "" {
			entry.ParentID = sliceID
		}
		if err := st.UpdateEntry(ctx, entry); err != nil {
			return err
		}
	} else {
		if err := st.AddEntry(ctx, entry); err != nil {
			return err
		}
	}

	return st.AddFileToSlice(ctx, fileID, sliceID)
}

func loadSliceFileStateFromParent(ctx context.Context, st Storage, slice *models.Slice, fileID string) (sliceFileState, error) {
	if slice == nil || strings.TrimSpace(slice.ParentSlice) == "" || slice.ParentSlice == slice.ID {
		return sliceFileState{}, nil
	}
	return loadSliceFileState(ctx, st, slice.ParentSlice, fileID)
}

func mergeParentState(ctx context.Context, st Storage, slice *models.Slice, fileID string, state sliceFileState) (sliceFileState, error) {
	if slice == nil || strings.TrimSpace(slice.ParentSlice) == "" || slice.ParentSlice == slice.ID {
		return state, nil
	}

	parentState, err := loadSliceFileState(ctx, st, slice.ParentSlice, fileID)
	if err != nil {
		return sliceFileState{}, err
	}
	if !parentState.exists {
		return state, nil
	}
	if !state.exists {
		return parentState, nil
	}
	if state.hash == "" {
		state.hash = parentState.hash
	}
	if !state.executable {
		state.executable = parentState.executable
	}
	if state.symlinkTarget == "" {
		state.symlinkTarget = parentState.symlinkTarget
	}
	return state, nil
}
