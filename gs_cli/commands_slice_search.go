package main

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/niczy/gitslice/internal/searchindex"
)

type sliceSearchMatch struct {
	Path       string `json:"path"`
	LineNumber int    `json:"line_number"`
	Line       string `json:"line"`
}

func handleSliceSearch(ctx context.Context, cli *CLI, args []string) {
	args, jsonRequested := consumeBoolFlag(args, "json")
	fs := newCommandFlagSet("slice search")
	regexMode := fs.Bool("regex", false, "Interpret the pattern as a regex")
	glob := fs.String("glob", "", "Restrict matches to relative paths matching the glob")
	jsonOutput := fs.Bool("json", false, "Print structured JSON output")
	parseFlagSetInterspersed(fs, args)
	jsonEnabled := jsonRequested || *jsonOutput
	if fs.NArg() != 1 {
		commandUsage("Usage: gs slice search <pattern> [--regex] [--glob <pattern>] [--json]")
		return
	}

	pattern := strings.TrimSpace(fs.Arg(0))
	if pattern == "" {
		commandFatal("INVALID_ARGUMENT", "Search pattern cannot be empty", false, "")
	}

	sliceID, err := sliceIDFromConfig()
	if err != nil {
		commandFatalf("SLICE_NOT_BOUND", false, "gs slice checkout <slice-id>", "Failed to read current slice binding: %v", err)
	}
	checkoutIndex, err := readCheckoutIndex(".")
	if err != nil {
		commandFatalf("CHECKOUT_METADATA_MISSING", false, "gs slice checkout <slice-id>", "Failed to read checkout index: %v", err)
	}
	if checkoutIndex == nil {
		commandFatal("CHECKOUT_METADATA_MISSING", "Cannot search slice: checkout metadata missing. Run gs slice checkout again.", false, "gs slice checkout <slice-id>")
	}

	baseArtifact, err := readLocalSliceSearchBaseArtifact(".")
	if err != nil {
		baseArtifact, err = buildLocalSliceSearchArtifactFromIndex(".", checkoutIndex)
		if err != nil {
			commandFatalf("SLICE_SEARCH_FAILED", false, "", "Failed to load local search artifact: %v", err)
		}
		if writeErr := writeLocalSliceSearchArtifact(".", baseArtifact, &localSliceSearchArtifactMetadata{
			Version:    baseArtifact.Version,
			SliceID:    checkoutIndex.SliceID,
			CommitHash: checkoutIndex.CommitHash,
			Source:     "rebuilt_local",
		}); writeErr != nil {
			commandFatalf("SLICE_SEARCH_FAILED", false, "", "Failed to write local search artifact: %v", writeErr)
		}
	}

	statusEntries, err := collectNoGitWorkingTreeStatus(".", checkoutIndex)
	if err != nil {
		commandFatalf("SLICE_SEARCH_FAILED", false, "", "Failed to collect working tree status: %v", err)
	}
	overlayArtifact, overlayState, err := buildLocalSliceSearchOverlay(".", checkoutIndex, statusEntries)
	if err != nil {
		commandFatalf("SLICE_SEARCH_FAILED", false, "", "Failed to build local search overlay: %v", err)
	}
	if err := writeLocalSliceSearchOverlay(".", overlayArtifact, overlayState); err != nil {
		commandFatalf("SLICE_SEARCH_FAILED", false, "", "Failed to persist local search overlay: %v", err)
	}

	queryPattern := pattern
	if !*regexMode {
		queryPattern = regexp.QuoteMeta(pattern)
	}
	query, err := searchindex.BuildRegexQuery(queryPattern, nil, searchindex.SparseModeCovering)
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Invalid search pattern: %v", err)
	}

	matcher, err := newLocalSliceSearchMatcher(pattern, *regexMode)
	if err != nil {
		commandFatalf("INVALID_ARGUMENT", false, "", "Invalid search pattern: %v", err)
	}
	matches, err := runLocalSliceSearch(".", baseArtifact, overlayArtifact, overlayState, query, matcher, strings.TrimSpace(*glob))
	if err != nil {
		commandFatalf("SLICE_SEARCH_FAILED", false, "", "Failed to search slice: %v", err)
	}

	if jsonEnabled {
		writeJSONOutput(buildSliceSearchOutput(sliceID, pattern, *regexMode, *glob, matches))
		return
	}
	for _, match := range matches {
		fmt.Printf("%s:%d:%s\n", match.Path, match.LineNumber, match.Line)
	}
}

type localSliceSearchMatcher struct {
	pattern   string
	regexMode bool
	regex     *regexp.Regexp
}

func newLocalSliceSearchMatcher(pattern string, regexMode bool) (*localSliceSearchMatcher, error) {
	matcher := &localSliceSearchMatcher{
		pattern:   pattern,
		regexMode: regexMode,
	}
	if regexMode {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, err
		}
		matcher.regex = re
	}
	return matcher, nil
}

func (m *localSliceSearchMatcher) lineMatches(line string) bool {
	if m == nil {
		return false
	}
	if m.regexMode {
		return m.regex.MatchString(line)
	}
	return strings.Contains(line, m.pattern)
}

func runLocalSliceSearch(root string, baseArtifact, overlayArtifact *searchindex.SliceArtifact, overlayState *localSliceSearchOverlayState, query *searchindex.QueryNode, matcher *localSliceSearchMatcher, globPattern string) ([]sliceSearchMatch, error) {
	_ = query
	overlayOverrides := make(map[string]struct{})
	removedPaths := make(map[string]struct{})
	if overlayState != nil {
		for _, path := range overlayState.RemovedPaths {
			removedPaths[path] = struct{}{}
		}
	}
	if overlayArtifact != nil {
		for _, file := range overlayArtifact.Files {
			overlayOverrides[file.Path] = struct{}{}
		}
	}

	candidatePaths := make([]string, 0)
	if baseArtifact != nil {
		for _, file := range baseArtifact.Files {
			path := file.Path
			if _, removed := removedPaths[path]; removed {
				continue
			}
			if _, overridden := overlayOverrides[path]; overridden {
				continue
			}
			if !sliceSearchPathMatchesGlob(path, globPattern) {
				continue
			}
			candidatePaths = append(candidatePaths, path)
		}
	}
	if overlayArtifact != nil {
		for _, file := range overlayArtifact.Files {
			path := file.Path
			if !sliceSearchPathMatchesGlob(path, globPattern) {
				continue
			}
			candidatePaths = append(candidatePaths, path)
		}
	}
	candidatePaths = uniqueCheckoutPaths(candidatePaths)

	matches := make([]sliceSearchMatch, 0)
	for _, relPath := range candidatePaths {
		fileMatches, err := verifyLocalSliceSearchMatches(root, relPath, matcher)
		if err != nil {
			return nil, err
		}
		matches = append(matches, fileMatches...)
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Path != matches[j].Path {
			return matches[i].Path < matches[j].Path
		}
		return matches[i].LineNumber < matches[j].LineNumber
	})
	return matches, nil
}

func sliceSearchPathMatchesGlob(relPath, pattern string) bool {
	if strings.TrimSpace(pattern) == "" {
		return true
	}
	matched, err := path.Match(pattern, filepath.ToSlash(relPath))
	if err != nil {
		return true
	}
	return matched
}

func verifyLocalSliceSearchMatches(root, relPath string, matcher *localSliceSearchMatcher) ([]sliceSearchMatch, error) {
	absPath := filepath.Join(root, filepath.FromSlash(relPath))
	info, err := os.Lstat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nil
	}
	content, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	if !searchindex.IsIndexableText(content) {
		return nil, nil
	}
	lines := strings.Split(string(content), "\n")
	matches := make([]sliceSearchMatch, 0)
	for i, line := range lines {
		if !matcher.lineMatches(line) {
			continue
		}
		matches = append(matches, sliceSearchMatch{
			Path:       relPath,
			LineNumber: i + 1,
			Line:       line,
		})
	}
	return matches, nil
}
