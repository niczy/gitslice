package storage

import "github.com/niczy/gitslice/internal/models"

func cloneManifest(manifest *models.FileManifest) *models.FileManifest {
	if manifest == nil {
		return nil
	}
	copyManifest := *manifest
	if len(manifest.Blocks) > 0 {
		copyManifest.Blocks = append([]models.Block(nil), manifest.Blocks...)
	}
	return &copyManifest
}
