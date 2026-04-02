package filesystemservice

import (
	"encoding/json"
	"expvar"
	"strings"
	"time"

	"github.com/niczy/gitslice/internal/models"
)

var (
	metricFilesystemBlocksWrittenTotal   = expvar.NewInt("filesystem_blocks_written_total")
	metricFilesystemBlocksReusedTotal    = expvar.NewInt("filesystem_blocks_reused_total")
	metricFilesystemManifestWrites       = expvar.NewInt("filesystem_manifest_writes_total")
	metricFilesystemManifestBytesTotal   = expvar.NewInt("filesystem_manifest_bytes_total")
	metricFilesystemSearchCandidateFiles = expvar.NewMap("filesystem_search_candidate_files_total")
	metricFilesystemSearchCandidateCount = expvar.NewMap("filesystem_search_candidate_query_count")
	metricFilesystemSearchVerifyMillis   = expvar.NewMap("filesystem_search_exact_verify_millis_total")
	metricFilesystemSearchVerifyCount    = expvar.NewMap("filesystem_search_exact_verify_count")
	metricFilesystemSearchFallbackTotal  = expvar.NewMap("filesystem_search_fallback_total")
	metricFilesystemSearchArtifactLoadMs = expvar.NewMap("filesystem_search_artifact_load_millis_total")
	metricFilesystemSearchArtifactLoads  = expvar.NewMap("filesystem_search_artifact_load_count")
	metricFilesystemDedupRatio           = expvar.Func(func() any {
		return filesystemDedupRatio(metricFilesystemBlocksWrittenTotal.Value(), metricFilesystemBlocksReusedTotal.Value())
	})
)

type filesystemMetricsSnapshot struct {
	BlocksWrittenTotal   int64
	BlocksReusedTotal    int64
	ManifestWrites       int64
	ManifestBytesTotal   int64
	DedupRatio           float64
	SearchCandidateFiles string
	SearchCandidateCount string
	SearchVerifyMillis   string
	SearchVerifyCount    string
	SearchFallbackTotal  string
	SearchArtifactLoadMs string
	SearchArtifactLoads  string
}

func observeFilesystemBlocks(writtenBlocks, reusedBlocks int) {
	if writtenBlocks > 0 {
		metricFilesystemBlocksWrittenTotal.Add(int64(writtenBlocks))
	}
	if reusedBlocks > 0 {
		metricFilesystemBlocksReusedTotal.Add(int64(reusedBlocks))
	}
}

func observeFilesystemManifest(manifest *models.FileManifest) {
	metricFilesystemManifestWrites.Add(1)
	if manifest == nil {
		return
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return
	}
	metricFilesystemManifestBytesTotal.Add(int64(len(encoded)))
}

func observeFilesystemManifestWrite(manifest *models.FileManifest, writtenBlocks, reusedBlocks int) {
	observeFilesystemBlocks(writtenBlocks, reusedBlocks)
	observeFilesystemManifest(manifest)
}

func filesystemMetricLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer(" ", "_", ",", "_", "=", "_", "|", "_")
	return replacer.Replace(value)
}

func observeFilesystemSearchCandidates(mode string, count int) {
	key := filesystemMetricLabel(mode)
	metricFilesystemSearchCandidateCount.Add(key, 1)
	if count > 0 {
		metricFilesystemSearchCandidateFiles.Add(key, int64(count))
	}
}

func observeFilesystemSearchVerify(mode string, latency time.Duration) {
	key := filesystemMetricLabel(mode)
	millis := latency.Milliseconds()
	if millis < 0 {
		millis = 0
	}
	metricFilesystemSearchVerifyMillis.Add(key, millis)
	metricFilesystemSearchVerifyCount.Add(key, 1)
}

func observeFilesystemSearchFallback(reason string) {
	metricFilesystemSearchFallbackTotal.Add(filesystemMetricLabel(reason), 1)
}

func observeFilesystemSearchArtifactLoad(outcome string, latency time.Duration) {
	key := filesystemMetricLabel(outcome)
	millis := latency.Milliseconds()
	if millis < 0 {
		millis = 0
	}
	metricFilesystemSearchArtifactLoadMs.Add(key, millis)
	metricFilesystemSearchArtifactLoads.Add(key, 1)
}

func snapshotFilesystemMetrics() filesystemMetricsSnapshot {
	written := metricFilesystemBlocksWrittenTotal.Value()
	reused := metricFilesystemBlocksReusedTotal.Value()
	return filesystemMetricsSnapshot{
		BlocksWrittenTotal:   written,
		BlocksReusedTotal:    reused,
		ManifestWrites:       metricFilesystemManifestWrites.Value(),
		ManifestBytesTotal:   metricFilesystemManifestBytesTotal.Value(),
		DedupRatio:           filesystemDedupRatio(written, reused),
		SearchCandidateFiles: metricFilesystemSearchCandidateFiles.String(),
		SearchCandidateCount: metricFilesystemSearchCandidateCount.String(),
		SearchVerifyMillis:   metricFilesystemSearchVerifyMillis.String(),
		SearchVerifyCount:    metricFilesystemSearchVerifyCount.String(),
		SearchFallbackTotal:  metricFilesystemSearchFallbackTotal.String(),
		SearchArtifactLoadMs: metricFilesystemSearchArtifactLoadMs.String(),
		SearchArtifactLoads:  metricFilesystemSearchArtifactLoads.String(),
	}
}

func filesystemDedupRatio(written, reused int64) float64 {
	total := written + reused
	if total <= 0 {
		return 0
	}
	return float64(reused) / float64(total)
}
