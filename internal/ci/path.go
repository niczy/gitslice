package ci

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

func NormalizeHomePath(raw string) (string, error) {
	return normalizeLogicalPath(raw, false)
}

func normalizeHomePattern(baseDir, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("pattern is required")
	}
	if strings.HasPrefix(raw, "/") {
		return normalizeLogicalPath(raw, true)
	}
	baseDir, err := NormalizeHomePath(baseDir)
	if err != nil {
		return "", err
	}
	return normalizeLogicalPath(strings.TrimPrefix(baseDir, "/")+"/"+raw, true)
}

func NormalizeArtifactPattern(baseDir, raw string) (string, error) {
	return normalizeHomePattern(baseDir, raw)
}

func normalizeLogicalPath(raw string, allowGlob bool) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("path is required")
	}
	if strings.Contains(trimmed, "\x00") {
		return "", fmt.Errorf("path contains null byte")
	}
	if strings.Contains(trimmed, "\\") {
		return "", fmt.Errorf("path must use '/' separators")
	}
	if looksLikeHostPath(trimmed) {
		return "", fmt.Errorf("host paths are not allowed: %s", raw)
	}

	segments := strings.Split(strings.Trim(trimmed, "/"), "/")
	stack := make([]string, 0, len(segments))
	for _, segment := range segments {
		switch segment {
		case "", ".":
			continue
		case "..":
			if len(stack) == 0 {
				return "", fmt.Errorf("path escapes home root: %s", raw)
			}
			stack = stack[:len(stack)-1]
		case "~":
			return "", fmt.Errorf("host home segments are not allowed: %s", raw)
		default:
			if !allowGlob && strings.ContainsAny(segment, "*?[") {
				return "", fmt.Errorf("glob characters are not allowed in path: %s", raw)
			}
			stack = append(stack, segment)
		}
	}
	if len(stack) == 0 {
		return "/", nil
	}
	return "/" + strings.Join(stack, "/"), nil
}

func looksLikeHostPath(raw string) bool {
	if len(raw) >= 3 && raw[1] == ':' && (raw[2] == '/' || raw[2] == '\\') {
		return true
	}
	return strings.HasPrefix(raw, "~/")
}

func normalizeChangedPaths(paths []string) ([]string, error) {
	seen := make(map[string]struct{}, len(paths))
	normalized := make([]string, 0, len(paths))
	for _, raw := range paths {
		p, err := NormalizeHomePath(raw)
		if err != nil {
			return nil, fmt.Errorf("changed path %q: %w", raw, err)
		}
		if p == "/" {
			return nil, fmt.Errorf("changed path must not be the home root")
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		normalized = append(normalized, p)
	}
	sort.Strings(normalized)
	return normalized, nil
}

func ancestorManifestPaths(changedPaths []string) []string {
	seen := make(map[string]struct{})
	for _, changed := range changedPaths {
		dir := path.Dir(changed)
		for {
			manifestPath := path.Join(dir, FolderManifestName)
			if !strings.HasPrefix(manifestPath, "/") {
				manifestPath = "/" + manifestPath
			}
			seen[manifestPath] = struct{}{}
			if dir == "/" {
				break
			}
			dir = path.Dir(dir)
		}
	}
	paths := make([]string, 0, len(seen))
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

func manifestDir(manifestPath string) (string, error) {
	normalized, err := NormalizeHomePath(manifestPath)
	if err != nil {
		return "", err
	}
	if path.Base(normalized) != FolderManifestName {
		return "", fmt.Errorf("manifest path must end with %s: %s", FolderManifestName, manifestPath)
	}
	dir := path.Dir(normalized)
	if dir == "." {
		return "/", nil
	}
	return dir, nil
}

func pathWithinDir(candidate, dir string) bool {
	candidate, candidateErr := NormalizeHomePath(candidate)
	dir, dirErr := NormalizeHomePath(dir)
	if candidateErr != nil || dirErr != nil {
		return false
	}
	if dir == "/" {
		return candidate != "/"
	}
	return candidate == dir || strings.HasPrefix(candidate, dir+"/")
}

func matchManifestPatterns(patterns []string, baseDir, candidate string) (bool, error) {
	candidate, err := NormalizeHomePath(candidate)
	if err != nil {
		return false, err
	}
	for _, raw := range patterns {
		pattern, err := normalizeHomePattern(baseDir, raw)
		if err != nil {
			return false, err
		}
		if matchHomePattern(pattern, candidate) {
			return true, nil
		}
	}
	return false, nil
}

func matchHomePattern(pattern, candidate string) bool {
	pattern = strings.TrimSpace(pattern)
	candidate = strings.TrimSpace(candidate)
	if pattern == "" || candidate == "" {
		return false
	}
	if !strings.ContainsAny(pattern, "*?[") {
		if pattern == "/" {
			return candidate != "/"
		}
		return candidate == pattern || strings.HasPrefix(candidate, pattern+"/")
	}
	pattern = strings.TrimPrefix(pattern, "/")
	candidate = strings.TrimPrefix(candidate, "/")
	return matchGlobSegments(strings.Split(pattern, "/"), strings.Split(candidate, "/"))
}

func MatchHomePattern(pattern, candidate string) bool {
	return matchHomePattern(pattern, candidate)
}

func matchGlobSegments(patternSegments, candidateSegments []string) bool {
	if len(patternSegments) == 0 {
		return len(candidateSegments) == 0
	}
	current := patternSegments[0]
	if current == "**" {
		if matchGlobSegments(patternSegments[1:], candidateSegments) {
			return true
		}
		if len(candidateSegments) == 0 {
			return false
		}
		return matchGlobSegments(patternSegments, candidateSegments[1:])
	}
	if len(candidateSegments) == 0 {
		return false
	}
	matched, err := path.Match(current, candidateSegments[0])
	if err != nil || !matched {
		return false
	}
	return matchGlobSegments(patternSegments[1:], candidateSegments[1:])
}
