//go:build linux

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

func tryCloneCacheObject(dst, src *os.File) error {
	return unix.IoctlFileClone(int(dst.Fd()), int(src.Fd()))
}
