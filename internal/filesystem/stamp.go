package filesystem

import (
	"os"
	"strings"
)

// A stamp is what one round of the editor's file watch knows about one path,
// and it exists because the two questions that watch asks are not one question.
// Whether a file's content moved is the version token's answer, the same token
// the save already stands on (see version.go). Whether a directory's set of
// entries moved is that directory's listing, which is what the tree renders.
//
// The stat is the prefilter and never the decision. A path whose size and mtime
// are where the round before left them is neither read nor listed and keeps the
// token it had; only one whose stat moved is looked at, and the token says
// whether anything really happened. That the stat lies in both directions is
// precisely why it may not decide: a git checkout rewrites identical bytes and
// moves the timestamp, so deciding on the stat would report a change to
// everybody for nothing, and a coarse kernel clock cannot tell two writes inside
// one tick apart. A directory's mtime is the sharper of the two, it moves on a
// create, a delete and a rename inside it and on nothing else, which is exactly
// the semantics a lazily loaded tree needs and why a folder is no special case:
// a folder is an entry in its parent like every other one. Even there it is
// only the prefilter, because a timestamp cannot be compared with what a
// browser is showing and a listing can.
//
// That comparison is the point of the token being what it is. A stamp can be
// seeded from what a client says it holds, the version of the file it read or
// the signature of the listing it rendered, and the next round then answers
// "the disk is not what you are showing" instead of "nothing moved since I
// started looking". Without that, a path that joins the watch and is written a
// moment later is written into the very first reading of it and is never
// reported at all.
type Stamp struct {
	// Exists is false for everything the editor cannot answer for: a path that
	// is gone, one that is not the kind of thing it was watched as, and one
	// that escapes the project. All of those are a movement the tab or the tree
	// has to hear about, and none of them is an error worth its own case.
	Exists  bool
	Size    int64
	ModTime int64
	// Version is the token: a file's content hash, a directory's listing
	// signature. A file too large to read carries none, and where it is empty
	// the stat is all there is and Same falls back to it.
	Version string
}

// SeedStamp is a stamp taken from what a client says it holds rather than from
// the disk. Its stat is deliberately empty, so the next round cannot take the
// prefilter's word for it and has to look: that look is the comparison between
// the disk and the screen.
func SeedStamp(token string) Stamp {
	return Stamp{Exists: true, Version: token}
}

// Same reports whether two stamps describe the same state. The token decides
// wherever there is one, and the stat decides where there is none, which is
// what makes one type serve a file and a directory alike.
func (s Stamp) Same(other Stamp) bool {
	if s.Exists != other.Exists {
		return false
	}
	if !s.Exists {
		return true
	}
	if s.Version != "" || other.Version != "" {
		return s.Version == other.Version
	}
	return s.Size == other.Size && s.ModTime == other.ModTime
}

// StampFile probes one file, reading it only when the stat says it moved since
// last. A file that is gone, is not a regular file or lies outside the project
// answers the zero stamp, which is what a deleted file looks like to the tab
// that has it open.
//
// The read is not ReadFileText: what is watched here is whatever a tab holds,
// and a tab holds images and archives too. Binary content is a token like any
// other, only the size limit stands, and past it the stat is the token.
func StampFile(root, rel string, last Stamp) Stamp {
	target, err := ResolveUnder(root, rel)
	if err != nil {
		return Stamp{}
	}
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() {
		return Stamp{}
	}
	stamp := Stamp{Exists: true, Size: info.Size(), ModTime: info.ModTime().UnixNano()}
	if last.Exists && last.Size == stamp.Size && last.ModTime == stamp.ModTime {
		stamp.Version = last.Version
		return stamp
	}
	if stamp.Size > MaxEditableBytes {
		return stamp
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return stamp
	}
	stamp.Version = versionOf(data)
	return stamp
}

// StampDir probes one directory: the mtime says whether to look, the listing
// says what is there. Listing costs a ReadDir, which is why it only happens
// where the mtime moved, and that is exactly where something appeared,
// disappeared or was renamed.
func StampDir(root, rel string, last Stamp) Stamp {
	target, err := ResolveUnder(root, rel)
	if err != nil {
		return Stamp{}
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		return Stamp{}
	}
	stamp := Stamp{Exists: true, Size: info.Size(), ModTime: info.ModTime().UnixNano()}
	if last.Exists && last.Size == stamp.Size && last.ModTime == stamp.ModTime {
		stamp.Version = last.Version
		return stamp
	}
	entries, err := ListDir(root, rel)
	if err != nil {
		return stamp
	}
	stamp.Version = DirSignature(entries)
	return stamp
}

// DirSignature is the token of a directory listing: which entries it holds and
// which of them are folders, in the order ListDir answers them, which is the
// order the tree renders. It goes out with every listing so a client can hand
// it back and be told whether what it is showing is still the disk. Sizes and
// timestamps stay out on purpose: a write into a file it holds changes neither
// a row nor this.
func DirSignature(entries []Entry) string {
	var b strings.Builder
	for _, entry := range entries {
		b.WriteString(entry.Name)
		if entry.IsDir {
			b.WriteByte('/')
		}
		b.WriteByte('\n')
	}
	return versionOf([]byte(b.String()))
}
