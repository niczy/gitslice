package storage

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

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
	artifact, err := BuildSliceSearchArtifact(ctx, st, sliceID, commitHash)
	if err != nil {
		return nil, err
	}
	if err := StoreSliceSearchArtifact(ctx, st, artifact); err != nil {
		return nil, err
	}
	return artifact, nil
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

	payload, err := st.GetSliceSearchArtifact(ctx, workspaceID, commitHash, searchindex.CurrentArtifactVersion)
	switch {
	case err == nil:
		artifact, decodeErr := searchindex.DecodeSliceArtifact(payload)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if err := st.PutWorkspaceSearchArtifact(ctx, workspaceID, artifact.Version, payload); err != nil {
			return nil, err
		}
		return artifact, nil
	case err != nil && !errors.Is(err, ErrEntryNotFound):
		return nil, err
	}

	artifact, err := BuildAndStoreSliceSearchArtifact(ctx, st, workspaceID, commitHash)
	if err != nil {
		return nil, err
	}
	if err := StoreWorkspaceSearchArtifact(ctx, st, workspaceID, artifact); err != nil {
		return nil, err
	}
	return artifact, nil
}

func loadOrBuildSearchBlob(ctx context.Context, st Storage, searchHash string, content []byte) (*searchindex.FileBlob, error) {
	payload, err := st.GetSearchIndexFileBlob(ctx, searchindex.CurrentBlobVersion, searchHash)
	if err == nil {
		blob, decodeErr := searchindex.DecodeFileBlob(payload)
		if decodeErr != nil {
			return nil, decodeErr
		}
		return blob, nil
	}
	if !errors.Is(err, ErrEntryNotFound) {
		return nil, err
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
