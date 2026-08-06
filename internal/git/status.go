package git

import (
	"strings"
)

// statusArgs is the one status call this package makes. Version 2 of the
// porcelain format is the stable machine format, -z makes it NUL separated so a
// file name may contain anything a file name may contain, and -M asks for
// renames instead of a delete plus an add. Untracked files are listed one by
// one instead of collapsed to their directory: the commit panel picks per
// file, so a single line for a whole new folder would make its files
// unpickable, and the fingerprint has to move when a file appears inside a
// folder that was already untracked. Ignored files are not listed either way,
// so the usual heavyweights stay out.
var statusArgs = []string{"status", "--porcelain=v2", "-z", "--untracked-files=all", "-M"}

// FileStatus is one path git reports as changed. Index and Worktree are the two
// status codes of the porcelain format, one character each ("." for unchanged,
// "M", "A", "D", "R", "C", "T", "U", and "?" for untracked). From carries the
// path a rename or a copy came from.
type FileStatus struct {
	Path     string `json:"path"`
	Index    string `json:"index"`
	Worktree string `json:"worktree"`
	From     string `json:"from,omitempty"`
}

// withinPrefix cuts the repository relative paths back to the directory that
// was asked. A project below the repository root sees only what is under it,
// with paths that line up with its own file tree.
func withinPrefix(files []FileStatus, prefix string) []FileStatus {
	if prefix == "" {
		return files
	}
	kept := make([]FileStatus, 0, len(files))
	for _, f := range files {
		if !strings.HasPrefix(f.Path, prefix) {
			continue
		}
		f.Path = strings.TrimPrefix(f.Path, prefix)
		// A rename out of a directory the project cannot see has a source the
		// project has no path for. Naming the repository relative one would
		// read as a project path and point at the wrong file, so it names none.
		if strings.HasPrefix(f.From, prefix) {
			f.From = strings.TrimPrefix(f.From, prefix)
		} else {
			f.From = ""
		}
		kept = append(kept, f)
	}
	return kept
}

// parseStatus reads the NUL separated porcelain v2 output. Every record is one
// entry, except a rename or a copy, whose source path follows as a record of
// its own.
func parseStatus(out []byte) []FileStatus {
	files := []FileStatus{}
	records := strings.Split(string(out), "\x00")
	for i := 0; i < len(records); i++ {
		record := records[i]
		if record == "" {
			continue
		}
		switch {
		case strings.HasPrefix(record, "1 "):
			if file, ok := parseEntry(record, 9); ok {
				files = append(files, file)
			}
		case strings.HasPrefix(record, "2 "):
			file, ok := parseEntry(record, 10)
			// The source path of the rename or copy is the next record.
			if i+1 < len(records) {
				i++
				file.From = records[i]
			}
			if ok {
				files = append(files, file)
			}
		case strings.HasPrefix(record, "u "):
			if file, ok := parseEntry(record, 11); ok {
				files = append(files, file)
			}
		case strings.HasPrefix(record, "? "):
			files = append(files, FileStatus{Path: record[2:], Index: ".", Worktree: "?"})
		}
	}
	return files
}

// parseEntry reads one changed entry. fields is how many space separated fields
// the record has including the path, which is the last one and may itself
// contain spaces.
func parseEntry(record string, fields int) (FileStatus, bool) {
	parts := strings.SplitN(record, " ", fields)
	if len(parts) < fields {
		return FileStatus{}, false
	}
	codes := parts[1]
	if len(codes) != 2 {
		return FileStatus{}, false
	}
	path := parts[fields-1]
	if path == "" {
		return FileStatus{}, false
	}
	return FileStatus{Path: path, Index: string(codes[0]), Worktree: string(codes[1])}, true
}
