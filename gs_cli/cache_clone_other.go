//go:build !linux

package gscli

import (
	"errors"
	"os"
)

var errCacheCloneUnsupported = errors.New("cache clone unsupported")

func tryCloneCacheObject(dst, src *os.File) error {
	return errCacheCloneUnsupported
}
