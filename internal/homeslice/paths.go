package homeslice

import (
	"fmt"
	"path"
	"strings"

	"github.com/niczy/gitslice/internal/common"
)

// ResolveVisiblePath converts a user-visible absolute path into the stored home-slice path.
func ResolveVisiblePath(username, raw string, required bool) (string, string, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return "", "", fmt.Errorf("username is required")
	}

	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		if required {
			return "", "", fmt.Errorf("path is required")
		}
		rootPath := RelativeRootPath(username)
		return rootPath, VisibleRootPath(username), nil
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "", "", fmt.Errorf("path must be absolute")
	}

	cleaned := path.Clean(strings.ReplaceAll(trimmed, "\\", "/"))
	if cleaned == "." || cleaned == "/" {
		return "", "", fmt.Errorf("path must target /%s/...", username)
	}

	storedPath := strings.TrimPrefix(cleaned, "/")
	if err := common.ValidateFilePath(storedPath); err != nil {
		return "", "", err
	}
	if !ownsStoredPath(username, storedPath) {
		return "", "", fmt.Errorf("path must stay under %s", VisibleRootPath(username))
	}
	return storedPath, VisiblePathForStored(storedPath), nil
}

// ResolveVisiblePattern converts a user-visible absolute glob/search pattern into the stored home-slice pattern.
func ResolveVisiblePattern(username, raw string, required bool) (string, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return "", fmt.Errorf("username is required")
	}

	pattern := strings.TrimSpace(raw)
	if pattern == "" {
		if required {
			return "", fmt.Errorf("pattern is required")
		}
		return "", nil
	}
	if !strings.HasPrefix(pattern, "/") {
		return "", fmt.Errorf("pattern must be absolute")
	}
	if strings.Contains(pattern, "\x00") {
		return "", fmt.Errorf("pattern contains null byte")
	}
	if strings.Contains(pattern, "..") {
		return "", fmt.Errorf("pattern must not contain '..'")
	}

	cleaned := path.Clean(strings.ReplaceAll(pattern, "\\", "/"))
	if cleaned == "." || cleaned == "/" {
		return "", fmt.Errorf("pattern is required")
	}
	storedPattern := strings.TrimPrefix(cleaned, "/")
	if !ownsStoredPath(username, storedPattern) {
		return "", fmt.Errorf("pattern must stay under %s", VisibleRootPath(username))
	}
	return storedPattern, nil
}

// VisiblePathForStored converts a stored home-slice path into the user-visible absolute path.
func VisiblePathForStored(storedPath string) string {
	cleaned := common.CleanRelativePath(storedPath)
	if cleaned == "" {
		return "/"
	}
	return "/" + cleaned
}

func ownsStoredPath(username, storedPath string) bool {
	storedPath = common.CleanRelativePath(storedPath)
	root := RelativeRootPath(username)
	if storedPath == "" || root == "" {
		return false
	}
	return storedPath == root || strings.HasPrefix(storedPath, root+"/")
}
