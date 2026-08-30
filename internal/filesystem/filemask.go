package filesystem

import (
	"path"
	"strings"
)

// FileMask is the file name filter of a content search: a comma separated list
// of glob patterns, matched case insensitively, with * and ? as the wildcards.
//
// A pattern without a slash is matched against the file's base name, so "*.go"
// means the extension anywhere in the tree; a pattern with one is matched
// against the project relative path. A leading "!" turns a pattern into an
// exclusion: the inclusions are an or, the exclusions then take files back out
// again, and a mask holding nothing but exclusions includes everything else.
//
// The zero value lets every file through, which is what a search without a mask
// asks for.
type FileMask struct {
	include []string
	exclude []string
}

// ParseFileMask reads what the palette's file field holds. Whitespace around a
// pattern is trimmed and blank entries are dropped, so the trailing comma
// standing there while the next pattern is typed does not empty the answer.
func ParseFileMask(raw string) FileMask {
	var m FileMask
	for _, field := range strings.Split(raw, ",") {
		pattern := strings.TrimSpace(field)
		exclude := strings.HasPrefix(pattern, "!")
		if exclude {
			pattern = strings.TrimSpace(pattern[1:])
		}
		// Paths travel slash separated, and a backslash is path.Match's escape
		// character, so a separator typed the other way is turned into one
		// rather than escaping the character behind it.
		pattern = strings.TrimLeft(strings.ReplaceAll(pattern, "\\", "/"), "/")
		if pattern == "" {
			continue
		}
		pattern = strings.ToLower(pattern)
		if exclude {
			m.exclude = append(m.exclude, pattern)
			continue
		}
		m.include = append(m.include, pattern)
	}
	return m
}

// Empty reports whether the mask lets every file through. A search asks this
// before it matches anything, because a mask nobody typed should cost nothing.
func (m FileMask) Empty() bool {
	return len(m.include) == 0 && len(m.exclude) == 0
}

// Match reports whether the file at the project relative path rel passes the
// mask.
func (m FileMask) Match(rel string) bool {
	if m.Empty() {
		return true
	}
	lower := strings.ToLower(rel)
	name := lower[strings.LastIndexByte(lower, '/')+1:]
	if len(m.include) > 0 && !matchesAnyPattern(m.include, lower, name) {
		return false
	}
	return !matchesAnyPattern(m.exclude, lower, name)
}

// matchesAnyPattern answers whether one of the patterns matches, each against
// the path or the base name depending on whether it carries a slash. A pattern
// that is not a valid glob matches nothing rather than failing the search: the
// field is typed into, and half a bracket expression stands in it on the way to
// a whole one.
func matchesAnyPattern(patterns []string, lower, name string) bool {
	for _, pattern := range patterns {
		subject := name
		if strings.ContainsRune(pattern, '/') {
			subject = lower
		}
		if ok, err := path.Match(pattern, subject); ok && err == nil {
			return true
		}
	}
	return false
}
