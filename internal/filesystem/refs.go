package filesystem

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// FileRef is a mention of a file in some text, a path that names an existing
// regular file under the root, with the line and column it may carry. Start
// and End address the whole mention, location included, in UTF-16 units.
type FileRef struct {
	Start int
	End   int
	Path  string // slash separated, relative to the root
	Line  int    // 0 when the mention carries none
	Col   int    // 0 when the mention carries none
}

var (
	refToken    = regexp.MustCompile("[^\\s\"'`<>()\\[\\]{},;|]+")
	refLocation = regexp.MustCompile(`^(.+?)(?::(\d+))?(?::(\d+))?$`)
)

// FileRefFinder answers the mentions of existing files in a text, relative to
// root or absolute inside it. A bare word without a dot, a slash or a line is
// far more often a word than a file, and a name starting with ~ is the shell's
// home, so neither is looked up. What a name resolves to is kept for every
// later text, so text dense with the same paths costs one look each.
func FileRefFinder(root string) func(text string) []FileRef {
	type file struct {
		rel string
		ok  bool
	}
	files := map[string]file{}
	return func(text string) []FileRef {
		var refs []FileRef
		at, units := 0, 0
		for _, span := range refToken.FindAllStringIndex(text, -1) {
			units += UTF16Len(text[at:span[0]])
			at = span[0]
			token := strings.TrimRight(text[span[0]:span[1]], ".,:;!?")
			if token == "" || len(token) > 4096 || strings.Contains(token, "://") {
				continue
			}
			m := refLocation.FindStringSubmatch(token)
			name, line, col := m[1], atoiOrZero(m[2]), atoiOrZero(m[3])
			if (line == 0 && !strings.ContainsAny(name, "./")) || strings.HasPrefix(name, "~") {
				continue
			}
			f, seen := files[name]
			if !seen {
				rel, target, ok := RelUnder(root, name)
				if ok {
					info, err := os.Stat(target)
					ok = err == nil && info.Mode().IsRegular()
				}
				f = file{rel, ok}
				files[name] = f
			}
			if f.ok {
				refs = append(refs, FileRef{Start: units, End: units + UTF16Len(token), Path: f.rel, Line: line, Col: col})
			}
		}
		return refs
	}
}

// RelUnder answers the slash separated path of name relative to root and its
// target on the disk, for a name relative to root or absolute inside it. Like
// ResolveUnder it refuses a name that leaves root, through a symlink too, and
// root itself. It refuses a backslash as well, which ResolveUnder reads as a
// slash, so the path would name another file than the name shows.
func RelUnder(root, name string) (string, string, bool) {
	if strings.Contains(name, `\`) {
		return "", "", false
	}
	if filepath.IsAbs(name) {
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return "", "", false
		}
		name = rel
	}
	rel := path.Clean(filepath.ToSlash(name))
	if rel == "." {
		return "", "", false
	}
	target, err := ResolveUnder(root, rel)
	if err != nil {
		return "", "", false
	}
	return rel, target, true
}

func atoiOrZero(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
