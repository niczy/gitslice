package sliceservice

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/niczy/gitslice/internal/models"
	"github.com/niczy/gitslice/internal/storage"
)

const maxThreeWayMergeMatrixCells = 4_000_000

type lineHunk struct {
	BaseStart int
	BaseEnd   int
	Lines     []string
}

type pendingAutoMerge struct {
	path       string
	content    []byte
	executable bool
}

func (s *sliceServiceServer) tryAutoMergeChangesetDrifts(ctx context.Context, cs *models.Changeset, drifts []changesetPathHeadDrift) (bool, error) {
	if cs == nil || len(drifts) == 0 {
		return false, nil
	}
	merges := make([]pendingAutoMerge, 0, len(drifts))
	for _, drift := range drifts {
		merge, ok, err := s.buildAutoMergeForDrift(ctx, drift)
		if err != nil || !ok {
			return false, err
		}
		merges = append(merges, merge)
	}
	for _, merge := range merges {
		if err := s.upsertSliceFilePathWithMetadata(ctx, cs.SliceID, merge.path, "", merge.content, merge.executable, ""); err != nil {
			return false, fmt.Errorf("write auto-merged content for %s: %w", merge.path, err)
		}
	}
	return len(merges) > 0, nil
}

func (s *sliceServiceServer) buildAutoMergeForDrift(ctx context.Context, drift changesetPathHeadDrift) (pendingAutoMerge, bool, error) {
	filePath := cleanDiffPath(drift.Path)
	if filePath == "" {
		return pendingAutoMerge{}, false, nil
	}
	baseHash := strings.TrimSpace(drift.BaseHash)
	oursHash := strings.TrimSpace(drift.OursHash)
	theirsHash := strings.TrimSpace(drift.CurrentHash)
	if baseHash == "" || oursHash == "" || theirsHash == "" {
		return pendingAutoMerge{}, false, nil
	}

	baseLines, baseOK, err := loadAutoMergeLinesFromHash(ctx, s.storage, baseHash)
	if err != nil || !baseOK {
		return pendingAutoMerge{}, false, err
	}
	oursLines, oursManifest, oursOK, err := loadAutoMergeLinesAndManifest(ctx, s.storage, oursHash)
	if err != nil || !oursOK {
		return pendingAutoMerge{}, false, err
	}
	theirsLines, theirsOK, err := loadAutoMergeLinesFromHash(ctx, s.storage, theirsHash)
	if err != nil || !theirsOK {
		return pendingAutoMerge{}, false, err
	}
	mergedLines, ok := threeWayMergeLines(baseLines, oursLines, theirsLines)
	if !ok {
		return pendingAutoMerge{}, false, nil
	}
	if oursManifest == nil {
		return pendingAutoMerge{}, false, nil
	}
	return pendingAutoMerge{
		path:       filePath,
		content:    []byte(strings.Join(mergedLines, "")),
		executable: oursManifest.Executable,
	}, true, nil
}

func loadAutoMergeLinesFromHash(ctx context.Context, st storage.Storage, hash string) ([]string, bool, error) {
	lines, _, ok, err := loadAutoMergeLinesAndManifest(ctx, st, hash)
	return lines, ok, err
}

func loadAutoMergeLinesAndManifest(ctx context.Context, st storage.Storage, hash string) ([]string, *models.FileManifest, bool, error) {
	cleaned := strings.TrimSpace(hash)
	if cleaned == "" {
		return nil, nil, false, nil
	}
	manifest, err := st.GetVersionedFileManifest(ctx, cleaned)
	if err != nil {
		if err == storage.ErrEntryNotFound {
			return nil, nil, false, nil
		}
		return nil, nil, false, err
	}
	if manifest == nil || strings.TrimSpace(manifest.SymlinkTarget) != "" {
		return nil, nil, false, nil
	}
	content, err := storage.ReadVersionedFileContent(ctx, st, cleaned)
	if err != nil || content == nil {
		return nil, nil, false, err
	}
	if !utf8.Valid(content.Content) || bytesContainsNUL(content.Content) {
		return nil, nil, false, nil
	}
	lines := strings.SplitAfter(string(content.Content), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, manifest, true, nil
}

func threeWayMergeLines(baseLines, oursLines, theirsLines []string) ([]string, bool) {
	if len(baseLines)*max(1, len(oursLines)) > maxThreeWayMergeMatrixCells ||
		len(baseLines)*max(1, len(theirsLines)) > maxThreeWayMergeMatrixCells {
		return nil, false
	}
	oursHunks := diffLineHunks(baseLines, oursLines)
	theirsHunks := diffLineHunks(baseLines, theirsLines)
	return mergeLineHunks(baseLines, oursHunks, theirsHunks)
}

func diffLineHunks(baseLines, changedLines []string) []lineHunk {
	n := len(baseLines)
	m := len(changedLines)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if baseLines[i] == changedLines[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	hunks := make([]lineHunk, 0)
	var current *lineHunk
	flush := func() {
		if current == nil {
			return
		}
		current.Lines = append([]string(nil), current.Lines...)
		hunks = append(hunks, *current)
		current = nil
	}
	ensure := func(baseIndex int) {
		if current == nil {
			current = &lineHunk{BaseStart: baseIndex, BaseEnd: baseIndex}
		}
	}

	i, j := 0, 0
	for i < n || j < m {
		if i < n && j < m && baseLines[i] == changedLines[j] {
			flush()
			i++
			j++
			continue
		}
		if j < m && (i == n || dp[i][j+1] >= dp[i+1][j]) {
			ensure(i)
			current.Lines = append(current.Lines, changedLines[j])
			j++
			continue
		}
		if i < n {
			ensure(i)
			i++
			current.BaseEnd = i
			continue
		}
	}
	flush()
	return hunks
}

func mergeLineHunks(baseLines []string, oursHunks, theirsHunks []lineHunk) ([]string, bool) {
	out := make([]string, 0, len(baseLines)+len(oursHunks)+len(theirsHunks))
	pos := 0
	i, j := 0, 0
	appendBaseUntil := func(next int) bool {
		if next < pos || next > len(baseLines) {
			return false
		}
		out = append(out, baseLines[pos:next]...)
		return true
	}

	for i < len(oursHunks) || j < len(theirsHunks) {
		if i < len(oursHunks) && j < len(theirsHunks) && oursHunks[i].BaseStart == theirsHunks[j].BaseStart {
			ours := oursHunks[i]
			theirs := theirsHunks[j]
			if !appendBaseUntil(ours.BaseStart) {
				return nil, false
			}
			if ours.BaseEnd == theirs.BaseEnd && equalStringSlices(ours.Lines, theirs.Lines) {
				out = append(out, ours.Lines...)
				pos = ours.BaseEnd
				i++
				j++
				continue
			}
			return nil, false
		}

		useOurs := j >= len(theirsHunks) || (i < len(oursHunks) && oursHunks[i].BaseStart < theirsHunks[j].BaseStart)
		hunk := lineHunk{}
		if useOurs {
			hunk = oursHunks[i]
			i++
		} else {
			hunk = theirsHunks[j]
			j++
		}
		if !appendBaseUntil(hunk.BaseStart) {
			return nil, false
		}
		out = append(out, hunk.Lines...)
		pos = hunk.BaseEnd
	}
	if !appendBaseUntil(len(baseLines)) {
		return nil, false
	}
	return out, true
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
