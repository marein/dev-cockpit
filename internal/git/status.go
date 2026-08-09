package git

import (
	"strconv"
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
// so the usual heavyweights stay out. --branch puts the branch headers in
// front of the entries, which is what names the branch and counts ahead and
// behind in the same round the changes come from; it also makes the worktree
// fingerprint move when the upstream does, so a fetch from anywhere reaches
// every open editor through the ordinary poll.
var statusArgs = []string{"status", "--porcelain=v2", "-z", "--branch", "--untracked-files=all", "-M"}

// BranchInfo is what the status headers say about where HEAD stands: the
// branch, its upstream, and how far the two have drifted apart. Counted says
// whether git could count at all, which it cannot without an upstream or with
// an upstream whose ref is gone. A detached HEAD has no branch name and
// carries its abbreviated commit instead.
type BranchInfo struct {
	Name     string `json:"name"`
	Detached bool   `json:"detached,omitempty"`
	Upstream string `json:"upstream,omitempty"`
	Ahead    int    `json:"ahead"`
	Behind   int    `json:"behind"`
	Counted  bool   `json:"counted,omitempty"`
}

// parseBranch reads the branch headers out of the status output. They are
// ordinary NUL separated records starting with "# ", written in front of the
// entries, and only that leading block is read: the source path of a rename
// follows its entry as a record of its own and is the bare path, so a file
// named "# branch.head something" would otherwise be taken for a header and
// name a branch nobody is on. Reading the front of the output is what the
// format guarantees, guessing from a prefix is not.
func parseBranch(out []byte) BranchInfo {
	info := BranchInfo{}
	oid := ""
	for _, rec := range strings.Split(string(out), "\x00") {
		if rec == "" {
			continue
		}
		if !strings.HasPrefix(rec, "# ") {
			break
		}
		key, value, _ := strings.Cut(rec[2:], " ")
		switch key {
		case "branch.oid":
			oid = value
		case "branch.head":
			info.Name = value
		case "branch.upstream":
			info.Upstream = value
		case "branch.ab":
			// Counted says git could count, so it is set by a number that
			// arrived and never by the header alone: a record whose two fields
			// neither of them parsed says as little about the drift as a
			// missing header does, and a zero that means "nobody knows" reads
			// on the statusbar as a branch that is level with its upstream.
			for _, part := range strings.Fields(value) {
				n, err := strconv.Atoi(part[1:])
				if err != nil {
					continue
				}
				if strings.HasPrefix(part, "+") {
					info.Ahead = n
				} else {
					info.Behind = n
				}
				info.Counted = true
			}
		}
	}
	if info.Name == "(detached)" {
		info.Detached = true
		info.Name = shortSHA(oid)
	}
	return info
}

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
