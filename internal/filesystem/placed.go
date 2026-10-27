package filesystem

import "os"

// PlacedEntry is a file or folder of a tree named both ways: relative to the
// tree's root, slash separated, and as the absolute path on the disk.
type PlacedEntry struct {
	Rel    string
	Abs    string
	Folder bool
}

// EntriesUnder places the entries under root, in their order, and leaves out
// every one that does not resolve under root right now to what it was listed
// as, a regular file or a folder. The entries usually come out of an index
// that may be a little stale, so a file deleted since, a file turned into a
// folder or the other way round, or a path reached through a symlink that leads
// out of root is answered as gone rather than as a guess.
func EntriesUnder(root string, entries []QuickOpenEntry) []PlacedEntry {
	out := make([]PlacedEntry, 0, len(entries))
	for _, e := range entries {
		abs, err := ResolveUnder(root, e.Path)
		if err != nil {
			continue
		}
		info, err := os.Lstat(abs)
		if err != nil || (e.Folder && !info.IsDir()) || (!e.Folder && !info.Mode().IsRegular()) {
			continue
		}
		out = append(out, PlacedEntry{Rel: e.Path, Abs: abs, Folder: e.Folder})
	}
	return out
}
