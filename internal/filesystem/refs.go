package filesystem

import (
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// FileRef is a mention of a file in some text, a path that names an existing
// regular file under the root, with the line and column it may carry. Start
// and End address the whole mention, location included, in bytes.
type FileRef struct {
	Start int
	End   int
	Path  string // slash separated, relative to the root
	Line  int    // 0 when the mention carries none
	Col   int    // 0 when the mention carries none
}

// maxRefLength is the longest mention read as one, in bytes.
const maxRefLength = 4096

// SeparatorAt answers how many bytes the separator at text[i] takes, 0 when a
// token that may mention a file goes on there. White space, a control byte, a
// quote, a bracket and the punctuation of lists and pipes separate. Every
// other byte belongs to a token, each byte of a multi byte character too, but
// for a no-break space, which programs pad code with and a terminal shows as
// a space.
func SeparatorAt(text string, i int) int {
	if strings.HasPrefix(text[i:], noBreakSpace) {
		return len(noBreakSpace)
	}
	if refSeparators[text[i]] {
		return 1
	}
	return 0
}

const noBreakSpace = "\u00a0"

var refSeparators = func() (t [256]bool) {
	for b := range byte(' ' + 1) {
		t[b] = true
	}
	for _, b := range []byte("\x7f\"'`<>()[]{},;|") {
		t[b] = true
	}
	return t
}()

// fileRefsTTL is how long the answer for a name is kept. Output dense with the
// same paths costs one look per name and second, and a file a program just
// wrote links a second later at the latest.
const fileRefsTTL = time.Second

// fileRefsMax bounds how many names, and so folders, are kept between two
// expiries.
const fileRefsMax = 4096

// FileRefs answers the mentions of existing files under root, relative to it or
// absolute inside it. A bare word without a dot, a slash or a line is far more
// often a word than a file, a word without a letter is a time, a version or a
// size, and a name starting with ~ is the shell's home, so none is looked up.
// It is not safe for concurrent use.
type FileRefs struct {
	root  string
	files map[string]refFile
	dirs  map[string]bool // whether a folder stays under root
	since time.Time
}

type refFile struct {
	rel string
	ok  bool
}

func NewFileRefs(root string) *FileRefs {
	return &FileRefs{root: root, files: map[string]refFile{}, dirs: map[string]bool{}}
}

// Find answers the mentions in a text, in order and apart.
func (f *FileRefs) Find(text string) []FileRef {
	var refs []FileRef
	for i := 0; i < len(text); {
		if n := SeparatorAt(text, i); n > 0 {
			i += n
			continue
		}
		j := i + 1
		for j < len(text) && SeparatorAt(text, j) == 0 {
			j++
		}
		if ref, ok := f.Mention(text[i:j]); ok {
			ref.Start += i
			ref.End += i
			refs = append(refs, ref)
		}
		i = j
	}
	return refs
}

// Mention reads a token, a text without a separator, as a mention of a file
// from its start. Trailing punctuation ends a sentence, not a name.
func (f *FileRefs) Mention(token string) (FileRef, bool) {
	for len(token) > 0 && strings.IndexByte(".,:;!?", token[len(token)-1]) >= 0 {
		token = token[:len(token)-1]
	}
	if !strings.ContainsAny(token, "./:") || !strings.ContainsFunc(token, unicode.IsLetter) || len(token) > maxRefLength || strings.Contains(token, "://") {
		return FileRef{}, false
	}
	name, end, line, col := refLocation(token)
	if (line == 0 && !strings.ContainsAny(name, "./")) || strings.HasPrefix(name, "~") {
		return FileRef{}, false
	}
	file := f.lookup(name)
	if !file.ok {
		return FileRef{}, false
	}
	return FileRef{End: end, Path: file.rel, Line: line, Col: col}, true
}

// refLocation splits a token into a name and the :line and :column after it,
// where after a line may follow the rest of a grep -n line, a colon and no
// digit after it. It answers where the location ends, the token's end when
// there is none.
func refLocation(token string) (string, int, int, int) {
	digits := func(at int) int {
		end := at
		for end < len(token) && token[end] >= '0' && token[end] <= '9' {
			end++
		}
		return end
	}
	rest := func(at int) bool {
		return at == len(token) || token[at] == ':' && (at+1 == len(token) || token[at+1] < '0' || token[at+1] > '9')
	}
	for i := 1; i < len(token); i++ {
		if token[i] != ':' {
			continue
		}
		line := digits(i + 1)
		if line == i+1 {
			continue
		}
		if line < len(token) && token[line] == ':' {
			if col := digits(line + 1); col > line+1 && rest(col) {
				return token[:i], col, atoiOrZero(token[i+1 : line]), atoiOrZero(token[line+1 : col])
			}
		}
		if rest(line) {
			return token[:i], line, atoiOrZero(token[i+1 : line]), 0
		}
	}
	return token, len(token), 0, 0
}

func (f *FileRefs) lookup(name string) refFile {
	if now := time.Now(); now.Sub(f.since) > fileRefsTTL || len(f.files) >= fileRefsMax {
		clear(f.files)
		clear(f.dirs)
		f.since = now
	}
	file, seen := f.files[name]
	if !seen {
		rel, ok := relName(f.root, name)
		file = refFile{rel, ok && f.isFile(rel)}
		f.files[name] = file
	}
	return file
}

// isFile says whether rel names a regular file whose target stays under root.
// The symlinks of a folder are resolved once for every file in it, so a file
// that is no symlink costs one lstat.
func (f *FileRefs) isFile(rel string) bool {
	full := filepath.Join(f.root, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	switch {
	case err != nil:
		return false
	case info.Mode()&os.ModeSymlink != 0:
		info, err = os.Stat(full)
		_, escape := ResolveUnder(f.root, rel)
		return err == nil && escape == nil && info.Mode().IsRegular()
	case !info.Mode().IsRegular():
		return false
	}
	dir := path.Dir(rel)
	ok, seen := f.dirs[dir]
	if !seen {
		_, err := ResolveUnder(f.root, dir)
		ok = err == nil
		f.dirs[dir] = ok
	}
	return ok
}

// RelUnder answers the slash separated path of name relative to root and its
// target on the disk, for a name relative to root or absolute inside it. Like
// ResolveUnder it refuses a name that leaves root, through a symlink too, and
// root itself. It refuses a backslash as well, which ResolveUnder reads as a
// slash, so the path would name another file than the name shows.
func RelUnder(root, name string) (string, string, bool) {
	rel, ok := relName(root, name)
	if !ok {
		return "", "", false
	}
	target, err := ResolveUnder(root, rel)
	if err != nil {
		return "", "", false
	}
	return rel, target, true
}

// relName is RelUnder by the letters of name alone, so a name that lies
// outside root is never looked at on the disk.
func relName(root, name string) (string, bool) {
	if strings.Contains(name, `\`) {
		return "", false
	}
	if filepath.IsAbs(name) {
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return "", false
		}
		name = rel
	}
	rel := path.Clean(filepath.ToSlash(name))
	return rel, rel != "." && rel != ".." && !strings.HasPrefix(rel, "../")
}

func atoiOrZero(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
