package filesystem

import (
	"errors"
	"fmt"
	"hash/fnv"
	"os"
)

// A file's version is the token that travels between reading a file and writing
// it back: the load path answers it, the save carries it, and the write only
// happens while it still describes what lies on the disk. That is what keeps an
// hour old buffer in a browser from writing over what a coder did to the same
// working copy in the meantime.
//
// The token is a hash over the file's content, never its mtime plus size. Both
// would answer "is the disk still what this buffer was loaded from", and the
// timestamp answers it wrong in both directions. It comes from the kernel out of
// a coarse clock that moves with the timer tick, so two writes inside one tick
// that leave the size alone are indistinguishable and a real change would slip
// through. And the other way round, a git checkout rewrites files whose content
// is identical, so the timestamp moves where nothing changed and a conflict
// dialog would stand in front of somebody for no reason. A content hash has
// neither problem: equal content is equal token, whatever happened to the
// timestamps.
//
// The hash is FNV-1a from the standard library, fast and not cryptographic,
// which is what this is for: the token says whether the file moved, it
// authenticates nothing and defends against nobody, and the only party that
// could look for a collision is the person's own editor. On the load path it
// costs nothing at all, the file is read anyway; on the save path the server
// reads the file once before it writes and compares.
func versionOf(content []byte) string {
	h := fnv.New64a()
	// Documented never to return an error.
	_, _ = h.Write(content)
	return fmt.Sprintf("%016x", h.Sum64())
}

// ErrFileChanged and ErrFileDeleted are the two ways a versioned write is
// refused, and they are told apart because the ways out are not the same: a
// changed file can be read back over the buffer, a deleted one cannot and can
// only be written again as a new file. Neither of them ever writes.
var (
	ErrFileChanged = errors.New("The file changed on disk after it was opened.")
	ErrFileDeleted = errors.New("The file no longer exists on disk.")
)

// WriteFileTextIfUnchanged writes content to root/rel, but only while the file
// on disk is still the one version was taken from. A file somebody else wrote
// in the meantime is ErrFileChanged and a file that is gone is ErrFileDeleted;
// both leave the disk exactly as it is. The version of what was written comes
// back with the entry, so a second save right after the first asks nothing.
//
// An empty version is the create path and writes whatever is there. That is
// what a file created in the editor takes on its first save, before anything
// ever read it back, and it is what the answer to a deleted file is: writing
// the buffer as a new file is a decision somebody made in front of the dialog,
// not a save that overwrote something silently.
func WriteFileTextIfUnchanged(root, rel string, content []byte, version string) (Entry, string, error) {
	if version == "" {
		entry, err := WriteFileText(root, rel, content)
		if err != nil {
			return Entry{}, "", err
		}
		return entry, versionOf(content), nil
	}
	target, err := ResolveUnder(root, rel)
	if err != nil {
		return Entry{}, "", err
	}
	info, err := os.Stat(target)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// The deletion is its own refusal and not a version that happens to
		// differ: WriteFileText would put the file back, and whoever deleted it
		// outside the editor never asked for that.
		return Entry{}, "", ErrFileDeleted
	case err != nil:
		return Entry{}, "", err
	case info.IsDir():
		return Entry{}, "", errWriteDir
	case !info.Mode().IsRegular():
		return Entry{}, "", errors.New("Only regular files can be edited.")
	case info.Size() > MaxEditableBytes:
		return Entry{}, "", ErrTooLarge
	}
	current, err := os.ReadFile(target)
	if err != nil {
		return Entry{}, "", err
	}
	if versionOf(current) != version {
		return Entry{}, "", ErrFileChanged
	}
	entry, err := WriteFileText(root, rel, content)
	if err != nil {
		return Entry{}, "", err
	}
	// The token of what was just written, never of what a re-read would find: a
	// write that landed a moment later belongs to the next save's comparison,
	// and answering its token here would hand the browser a token for content
	// it does not hold.
	return entry, versionOf(content), nil
}
