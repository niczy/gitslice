package storage

import (
	"expvar"
	"fmt"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/searchindex"
)

var (
	metricSearchIndexArtifactBuildMillis = expvar.NewMap("search_index_artifact_build_millis_total")
	metricSearchIndexArtifactBuildCount  = expvar.NewMap("search_index_artifact_build_count")
	metricSearchIndexArtifactLoadMillis  = expvar.NewMap("search_index_artifact_load_millis_total")
	metricSearchIndexArtifactLoadCount   = expvar.NewMap("search_index_artifact_load_count")
	metricSearchIndexArtifactFilesTotal  = expvar.NewMap("search_index_artifact_files_total")
	metricSearchIndexBlobBuildTotal      = expvar.NewMap("search_index_blob_build_total")
)

func searchMetricLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer(" ", "_", ",", "_", "=", "_", "|", "_")
	return replacer.Replace(value)
}

func searchMetricKey(parts ...string) string {
	normalized := make([]string, 0, len(parts))
	for _, part := range parts {
		normalized = append(normalized, searchMetricLabel(part))
	}
	return strings.Join(normalized, "|")
}

func observeSearchArtifactBuild(scope string, outcome SearchArtifactLoadOutcome, latency time.Duration, artifact *searchindex.SliceArtifact) {
	key := searchMetricKey(scope, outcome.String())
	millis := latency.Milliseconds()
	if millis < 0 {
		millis = 0
	}
	metricSearchIndexArtifactBuildMillis.Add(key, millis)
	metricSearchIndexArtifactBuildCount.Add(key, 1)
	if artifact != nil {
		metricSearchIndexArtifactFilesTotal.Add(key, int64(len(artifact.Files)))
	}
}

func observeSearchArtifactLoad(scope string, outcome SearchArtifactLoadOutcome, latency time.Duration) {
	key := searchMetricKey(scope, outcome.String())
	millis := latency.Milliseconds()
	if millis < 0 {
		millis = 0
	}
	metricSearchIndexArtifactLoadMillis.Add(key, millis)
	metricSearchIndexArtifactLoadCount.Add(key, 1)
}

func observeSearchBlobBuild(outcome string) {
	metricSearchIndexBlobBuildTotal.Add(searchMetricLabel(outcome), 1)
}

func SearchIndexMetricsSnapshot() map[string]string {
	return map[string]string{
		"search_index_artifact_build_millis_total": metricSearchIndexArtifactBuildMillis.String(),
		"search_index_artifact_build_count":        metricSearchIndexArtifactBuildCount.String(),
		"search_index_artifact_load_millis_total":  metricSearchIndexArtifactLoadMillis.String(),
		"search_index_artifact_load_count":         metricSearchIndexArtifactLoadCount.String(),
		"search_index_artifact_files_total":        metricSearchIndexArtifactFilesTotal.String(),
		"search_index_blob_build_total":            metricSearchIndexBlobBuildTotal.String(),
	}
}

type SearchArtifactLoadOutcome string

const (
	SearchArtifactOutcomeHit     SearchArtifactLoadOutcome = "hit"
	SearchArtifactOutcomeBuilt   SearchArtifactLoadOutcome = "built"
	SearchArtifactOutcomeRebuilt SearchArtifactLoadOutcome = "rebuilt"
)

func (o SearchArtifactLoadOutcome) String() string {
	if strings.TrimSpace(string(o)) == "" {
		return "unknown"
	}
	return string(o)
}

func validateStoredArtifact(artifact *searchindex.SliceArtifact, sliceID, commitHash string) error {
	if artifact == nil {
		return fmt.Errorf("search artifact is nil")
	}
	if artifact.Version != searchindex.CurrentArtifactVersion {
		return fmt.Errorf("search artifact version mismatch: got %d want %d", artifact.Version, searchindex.CurrentArtifactVersion)
	}
	if strings.TrimSpace(sliceID) != "" && artifact.SliceID != strings.TrimSpace(sliceID) {
		return fmt.Errorf("search artifact slice mismatch: got %q want %q", artifact.SliceID, strings.TrimSpace(sliceID))
	}
	if strings.TrimSpace(commitHash) != "" && artifact.CommitHash != strings.TrimSpace(commitHash) {
		return fmt.Errorf("search artifact commit mismatch: got %q want %q", artifact.CommitHash, strings.TrimSpace(commitHash))
	}
	return nil
}
