package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/niczy/gitslice/internal/searchindex"
	slicev1 "github.com/niczy/gitslice/proto/slice"
)

func fetchSliceSearchArtifact(ctx context.Context, cli *CLI, sliceID, commitHash string) (*searchindex.SliceArtifact, error) {
	if cli == nil || cli.sliceClient == nil {
		return nil, fmt.Errorf("slice client is not configured")
	}

	resp, err := cli.sliceClient.GetSliceSearchArtifact(ctx, &slicev1.GetSliceSearchArtifactRequest{
		SliceId:    strings.TrimSpace(sliceID),
		CommitHash: strings.TrimSpace(commitHash),
		Version:    searchindex.CurrentArtifactVersion,
	})
	if err != nil {
		return nil, err
	}
	return searchindex.DecodeSliceArtifact(resp.GetArtifact())
}
