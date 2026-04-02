package sliceservice

import (
	"expvar"
	"strings"
	"time"
)

var (
	metricSliceSearchArtifactDownloadMillis = expvar.NewMap("slice_search_artifact_download_millis_total")
	metricSliceSearchArtifactDownloadCount  = expvar.NewMap("slice_search_artifact_download_count")
)

func sliceSearchMetricLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer(" ", "_", ",", "_", "=", "_", "|", "_")
	return replacer.Replace(value)
}

func observeSliceSearchArtifactDownload(outcome string, latency time.Duration) {
	key := sliceSearchMetricLabel(outcome)
	millis := latency.Milliseconds()
	if millis < 0 {
		millis = 0
	}
	metricSliceSearchArtifactDownloadMillis.Add(key, millis)
	metricSliceSearchArtifactDownloadCount.Add(key, 1)
}

func snapshotSliceSearchMetrics() map[string]string {
	return map[string]string{
		"slice_search_artifact_download_millis_total": metricSliceSearchArtifactDownloadMillis.String(),
		"slice_search_artifact_download_count":        metricSliceSearchArtifactDownloadCount.String(),
	}
}
