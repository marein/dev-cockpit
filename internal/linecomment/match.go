package linecomment

import (
	"path"
	"strings"
)

// matchPath answers whether a comment's path matches one path filter value:
// the exact path, everything under it as a directory, or a glob where *
// matches inside one path segment and ** crosses segments, so `internal`
// covers a folder and `**/assistant.go` a name wherever it sits. A value that
// is no valid pattern matches nothing.
func matchPath(pattern, target string) bool {
	pattern = strings.TrimSuffix(pattern, "/")
	if pattern == "" {
		return false
	}
	if !strings.ContainsAny(pattern, "*?[") {
		return target == pattern || strings.HasPrefix(target, pattern+"/")
	}
	return globSegments(strings.Split(pattern, "/"), strings.Split(target, "/"))
}

func globSegments(pattern, parts []string) bool {
	if len(pattern) == 0 {
		return len(parts) == 0
	}
	if pattern[0] == "**" {
		for i := 0; i <= len(parts); i++ {
			if globSegments(pattern[1:], parts[i:]) {
				return true
			}
		}
		return false
	}
	if len(parts) == 0 {
		return false
	}
	if ok, err := path.Match(pattern[0], parts[0]); err != nil || !ok {
		return false
	}
	return globSegments(pattern[1:], parts[1:])
}

// matchesAnyPath answers whether target matches one of the filter values.
func matchesAnyPath(patterns []string, target string) bool {
	for _, pattern := range patterns {
		if matchPath(pattern, target) {
			return true
		}
	}
	return false
}
