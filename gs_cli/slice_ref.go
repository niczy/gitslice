package main

import (
	"context"
	"fmt"
	"strings"

	slicev1 "github.com/niczy/gitslice/proto/slice"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func resolveSliceRef(ctx context.Context, cli *CLI, input string) (string, error) {
	slug := strings.TrimSpace(input)
	if slug == "" {
		return "", fmt.Errorf("slice ID or ref is required")
	}

	// Qualified refs use `owner/slug`, so prefer slug resolution before
	// treating slash-containing refs as literal slice IDs.
	if strings.Contains(slug, "/") {
		resp, err := cli.sliceClient.GetSliceBySlug(ctx, &slicev1.GetSliceBySlugRequest{Slug: slug})
		if err == nil {
			return normalizeSliceID(resp.GetSliceId())
		}
		if status.Code(err) != codes.NotFound {
			return "", fmt.Errorf("unable to resolve slice slug %q: %w", slug, err)
		}
	}

	if sliceID, err := normalizeSliceID(slug); err == nil {
		return sliceID, nil
	}

	resp, err := cli.sliceClient.GetSliceBySlug(ctx, &slicev1.GetSliceBySlugRequest{Slug: slug})
	if err != nil {
		return "", fmt.Errorf("unable to resolve slice slug %q: %w", slug, err)
	}

	return normalizeSliceID(resp.GetSliceId())
}
