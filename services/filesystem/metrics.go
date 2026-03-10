package filesystemservice

import (
	"encoding/json"
	"expvar"

	"github.com/niczy/gitslice/internal/models"
)

var (
	metricFilesystemBlocksWrittenTotal = expvar.NewInt("filesystem_blocks_written_total")
	metricFilesystemBlocksReusedTotal  = expvar.NewInt("filesystem_blocks_reused_total")
	metricFilesystemManifestWrites     = expvar.NewInt("filesystem_manifest_writes_total")
	metricFilesystemManifestBytesTotal = expvar.NewInt("filesystem_manifest_bytes_total")
	metricFilesystemDedupRatio         = expvar.Func(func() any {
		return filesystemDedupRatio(metricFilesystemBlocksWrittenTotal.Value(), metricFilesystemBlocksReusedTotal.Value())
	})
)

type filesystemMetricsSnapshot struct {
	BlocksWrittenTotal int64
	BlocksReusedTotal  int64
	ManifestWrites     int64
	ManifestBytesTotal int64
	DedupRatio         float64
}

func observeFilesystemManifestWrite(manifest *models.FileManifest, writtenBlocks, reusedBlocks int) {
	if writtenBlocks > 0 {
		metricFilesystemBlocksWrittenTotal.Add(int64(writtenBlocks))
	}
	if reusedBlocks > 0 {
		metricFilesystemBlocksReusedTotal.Add(int64(reusedBlocks))
	}
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

func snapshotFilesystemMetrics() filesystemMetricsSnapshot {
	written := metricFilesystemBlocksWrittenTotal.Value()
	reused := metricFilesystemBlocksReusedTotal.Value()
	return filesystemMetricsSnapshot{
		BlocksWrittenTotal: written,
		BlocksReusedTotal:  reused,
		ManifestWrites:     metricFilesystemManifestWrites.Value(),
		ManifestBytesTotal: metricFilesystemManifestBytesTotal.Value(),
		DedupRatio:         filesystemDedupRatio(written, reused),
	}
}

func filesystemDedupRatio(written, reused int64) float64 {
	total := written + reused
	if total <= 0 {
		return 0
	}
	return float64(reused) / float64(total)
}
