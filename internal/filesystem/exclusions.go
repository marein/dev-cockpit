package filesystem

import (
	"sort"
	"strings"
)

// Exclusions are the directories a recursive walk stays out of: version control
// internals, vendored dependencies, build output. They are configured per
// install on the editor's search settings, because which folders are noise is a
// property of the project, not something the editor can know.
//
// Two kinds of entry:
//
//   - a bare name like "vendor" or "node_modules" excludes every directory with
//     that name, at any depth
//   - a path like "tests/_output" excludes exactly that directory, relative to
//     the project root
//
// Excluding a directory keeps the walk out of it entirely, so nothing below it
// is indexed or searched either.
type Exclusions struct {
	names map[string]bool
	paths map[string]bool
}

// DefaultExclusions are what applies when nothing has been configured: only
// git's own storage, which is never something you open in an editor and whose
// loose object files can outnumber the project itself.
//
// Dependency folders are deliberately not in here. They are code you do read,
// stepping into a library to see what it does is a normal thing to want, and the
// index carries them without trouble. What they cost is relevance rather than
// time, so excluding them is a choice each project makes on the search settings.
var DefaultExclusions = []string{".git"}

// ParseExclusions reads a stored or submitted list. Entries are separated by
// newlines or commas; surrounding whitespace and slashes are trimmed, blank
// entries and duplicates are dropped. Anything that would exclude the project
// root itself is ignored, because a walk of nothing is never what someone meant.
func ParseExclusions(raw string) Exclusions {
	ex := Exclusions{names: map[string]bool{}, paths: map[string]bool{}}
	for _, field := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == '\t'
	}) {
		entry := strings.Trim(strings.TrimSpace(field), "/")
		if entry == "" || entry == "." || entry == ".." {
			continue
		}
		// A path that tries to climb out of the project is not a folder in it.
		if strings.HasPrefix(entry, "../") || strings.Contains(entry, "/../") {
			continue
		}
		if strings.Contains(entry, "/") {
			ex.paths[entry] = true
			continue
		}
		ex.names[entry] = true
	}
	return ex
}

// DefaultExclusionSet is ParseExclusions over DefaultExclusions.
func DefaultExclusionSet() Exclusions {
	return ParseExclusions(strings.Join(DefaultExclusions, "\n"))
}

// SkipDir reports whether a walk should stay out of the directory at the given
// root-relative path. name is its base name.
func (e Exclusions) SkipDir(rel, name string) bool {
	return e.names[name] || e.paths[rel]
}

// List returns the entries in a stable order, names first, then paths, each
// alphabetically. It is what the settings form shows and what gets stored, so
// the value a person sees is the value that applies.
func (e Exclusions) List() []string {
	names := make([]string, 0, len(e.names))
	for n := range e.names {
		names = append(names, n)
	}
	paths := make([]string, 0, len(e.paths))
	for p := range e.paths {
		paths = append(paths, p)
	}
	sort.Strings(names)
	sort.Strings(paths)
	return append(names, paths...)
}

// String is the canonical stored form: one entry per line.
func (e Exclusions) String() string {
	return strings.Join(e.List(), "\n")
}

// Len reports how many entries there are.
func (e Exclusions) Len() int { return len(e.names) + len(e.paths) }

// Equal reports whether two sets exclude the same things. The quick open index
// uses it to notice that the setting changed and rebuild.
func (e Exclusions) Equal(other Exclusions) bool {
	if len(e.names) != len(other.names) || len(e.paths) != len(other.paths) {
		return false
	}
	for n := range e.names {
		if !other.names[n] {
			return false
		}
	}
	for p := range e.paths {
		if !other.paths[p] {
			return false
		}
	}
	return true
}
