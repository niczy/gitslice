package storage

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/searchindex"
)

func BuildSliceSearchArtifact(ctx context.Context, st Storage, sliceID, commitHash string) (*searchindex.SliceArtifact, error) {
	snapshot, err := st.GetCommitSnapshot(ctx, strings.TrimSpace(commitHash))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(sliceID) == "" {
		sliceID = snapshot.SliceID
	}
	if snapshot.SliceID != "" && sliceID != snapshot.SliceID {
		return nil, ErrInvalidInput
	}

	paths := make([]string, 0, len(snapshot.Files))
	for filePath := range snapshot.Files {
		cleaned := strings.TrimSpace(filePath)
		if cleaned == "" {
			continue
		}
		paths = append(paths, cleaned)
	}
	sort.Strings(paths)

	blobCache := make(map[string]*searchindex.FileBlob)
	inputs := make([]searchindex.ArtifactInputFile, 0, len(paths))
	for _, filePath := range paths {
		manifestHash := strings.TrimSpace(snapshot.Files[filePath])
		if manifestHash == "" {
			continue
		}
		manifest, err := st.GetVersionedFileManifest(ctx, manifestHash)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(manifest.SymlinkTarget) != "" {
			continue
		}

		content, err := ReadManifestContent(ctx, st, manifest)
		if err != nil {
			return nil, err
		}
		if !searchindex.IsIndexableText(content.Content) {
			continue
		}

		searchHash := searchindex.SearchContentHash(content.Content)
		blob, ok := blobCache[searchHash]
		if !ok {
			blob, err = loadOrBuildSearchBlob(ctx, st, searchHash, content.Content)
			if err != nil {
				return nil, err
			}
			blobCache[searchHash] = blob
		}

		inputs = append(inputs, searchindex.ArtifactInputFile{
			Path:              filePath,
			SearchContentHash: searchHash,
			NGrams:            append([]string(nil), blob.NGrams...),
		})
	}

	return searchindex.BuildSliceArtifact(sliceID, commitHash, inputs), nil
}

func StoreSliceSearchArtifact(ctx context.Context, st Storage, artifact *searchindex.SliceArtifact) error {
	if artifact == nil {
		return ErrInvalidInput
	}
	payload, err := searchindex.EncodeSliceArtifact(artifact)
	if err != nil {
		return err
	}
	return st.PutSliceSearchArtifact(ctx, artifact.SliceID, artifact.CommitHash, artifact.Version, payload)
}

func BuildAndStoreSliceSearchArtifact(ctx context.Context, st Storage, sliceID, commitHash string) (*searchindex.SliceArtifact, error) {
	startedAt := time.Now()
	artifact, err := BuildSliceSearchArtifact(ctx, st, sliceID, commitHash)
	if err != nil {
		return nil, err
	}
	if err := StoreSliceSearchArtifact(ctx, st, artifact); err != nil {
		return nil, err
	}
	observeSearchArtifactBuild("slice", SearchArtifactOutcomeBuilt, time.Since(startedAt), artifact)
	return artifact, nil
}

func LoadOrBuildSliceSearchArtifact(ctx context.Context, st Storage, sliceID, commitHash string) (*searchindex.SliceArtifact, SearchArtifactLoadOutcome, error) {
	startedAt := time.Now()

	payload, err := st.GetSliceSearchArtifact(ctx, sliceID, commitHash, searchindex.CurrentArtifactVersion)
	switch {
	case err == nil:
		artifact, decodeErr := searchindex.DecodeSliceArtifact(payload)
		if decodeErr == nil && validateStoredArtifact(artifact, sliceID, commitHash) == nil {
			observeSearchArtifactLoad("slice", SearchArtifactOutcomeHit, time.Since(startedAt))
			return artifact, SearchArtifactOutcomeHit, nil
		}
	case err != nil && !errors.Is(err, ErrEntryNotFound):
		return nil, "", err
	}

	outcome := SearchArtifactOutcomeBuilt
	if err == nil {
		outcome = SearchArtifactOutcomeRebuilt
	}
	buildStartedAt := time.Now()
	artifact, buildErr := BuildSliceSearchArtifact(ctx, st, sliceID, commitHash)
	if buildErr != nil {
		return nil, "", buildErr
	}
	if err := StoreSliceSearchArtifact(ctx, st, artifact); err != nil {
		return nil, "", err
	}
	observeSearchArtifactBuild("slice", outcome, time.Since(buildStartedAt), artifact)
	observeSearchArtifactLoad("slice", outcome, time.Since(startedAt))
	return artifact, outcome, nil
}

func StoreWorkspaceSearchArtifact(ctx context.Context, st Storage, workspaceID string, artifact *searchindex.SliceArtifact) error {
	if artifact == nil || strings.TrimSpace(workspaceID) == "" {
		return ErrInvalidInput
	}
	payload, err := searchindex.EncodeSliceArtifact(artifact)
	if err != nil {
		return err
	}
	return st.PutWorkspaceSearchArtifact(ctx, workspaceID, artifact.Version, payload)
}

func BuildAndStoreWorkspaceSearchArtifact(ctx context.Context, st Storage, workspaceID, commitHash string) (*searchindex.SliceArtifact, error) {
	if strings.TrimSpace(workspaceID) == "" {
		return nil, ErrInvalidInput
	}
	if strings.TrimSpace(commitHash) == "" {
		meta, err := st.GetSliceMetadata(ctx, workspaceID)
		if err != nil {
			return nil, err
		}
		commitHash = strings.TrimSpace(meta.HeadCommitHash)
	}
	if strings.TrimSpace(commitHash) == "" {
		artifact := searchindex.BuildSliceArtifact(workspaceID, "", nil)
		if err := StoreWorkspaceSearchArtifact(ctx, st, workspaceID, artifact); err != nil {
			return nil, err
		}
		return artifact, nil
	}

	artifact, _, err := LoadOrBuildSliceSearchArtifact(ctx, st, workspaceID, commitHash)
	if err != nil {
		return nil, err
	}
	if err := StoreWorkspaceSearchArtifact(ctx, st, workspaceID, artifact); err != nil {
		return nil, err
	}
	return artifact, nil
}

func LoadOrBuildWorkspaceSearchArtifact(ctx context.Context, st Storage, workspaceID, commitHash string) (*searchindex.SliceArtifact, SearchArtifactLoadOutcome, error) {
	startedAt := time.Now()

	commitHash = strings.TrimSpace(commitHash)
	if commitHash == "" {
		meta, err := st.GetSliceMetadata(ctx, workspaceID)
		if err != nil {
			return nil, "", err
		}
		commitHash = strings.TrimSpace(meta.HeadCommitHash)
	}
	if commitHash == "" {
		artifact := searchindex.BuildSliceArtifact(workspaceID, "", nil)
		if err := StoreWorkspaceSearchArtifact(ctx, st, workspaceID, artifact); err != nil {
			return nil, "", err
		}
		observeSearchArtifactLoad("workspace", SearchArtifactOutcomeBuilt, time.Since(startedAt))
		return artifact, SearchArtifactOutcomeBuilt, nil
	}

	payload, err := st.GetWorkspaceSearchArtifact(ctx, workspaceID, searchindex.CurrentArtifactVersion)
	switch {
	case err == nil:
		artifact, decodeErr := searchindex.DecodeSliceArtifact(payload)
		if decodeErr == nil && validateStoredArtifact(artifact, workspaceID, commitHash) == nil {
			observeSearchArtifactLoad("workspace", SearchArtifactOutcomeHit, time.Since(startedAt))
			return artifact, SearchArtifactOutcomeHit, nil
		}
	case err != nil && !errors.Is(err, ErrEntryNotFound):
		return nil, "", err
	}

	outcome := SearchArtifactOutcomeBuilt
	if err == nil {
		outcome = SearchArtifactOutcomeRebuilt
	}
	artifact, buildErr := BuildAndStoreWorkspaceSearchArtifact(ctx, st, workspaceID, commitHash)
	if buildErr != nil {
		return nil, "", buildErr
	}
	observeSearchArtifactLoad("workspace", outcome, time.Since(startedAt))
	return artifact, outcome, nil
}

func loadOrBuildSearchBlob(ctx context.Context, st Storage, searchHash string, content []byte) (*searchindex.FileBlob, error) {
	payload, err := st.GetSearchIndexFileBlob(ctx, searchindex.CurrentBlobVersion, searchHash)
	if err == nil {
		blob, decodeErr := searchindex.DecodeFileBlob(payload)
		if decodeErr == nil {
			return blob, nil
		}
		observeSearchBlobBuild("rebuilt")
	} else if !errors.Is(err, ErrEntryNotFound) {
		return nil, err
	}
	if errors.Is(err, ErrEntryNotFound) {
		observeSearchBlobBuild("built")
	}

	blob, err := searchindex.BuildFileBlob(content, searchindex.DefaultWeighter(), searchindex.SparseModeCovering)
	if err != nil {
		return nil, err
	}
	payload, err = searchindex.EncodeFileBlob(blob)
	if err != nil {
		return nil, err
	}
	if err := st.PutSearchIndexFileBlob(ctx, blob.Version, searchHash, payload); err != nil {
		return nil, fmt.Errorf("store search blob %s: %w", searchHash, err)
	}
	return blob, nil
}
