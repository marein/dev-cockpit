package filesystem

import "os"

// PlacedFile is a file of a tree named both ways: relative to the tree's root,
// slash separated, and as the absolute path on the disk.
type PlacedFile struct {
	Rel string
	Abs string
}

// FilesUnder places the relative paths under root, in their order, and leaves
// out every one that does not resolve to a regular file under root right now.
// The paths usually come out of an index that may be a little stale, so a
// file deleted since, turned into a folder or reached through a symlink that
// leads out of root is answered as gone rather than as a guess.
func FilesUnder(root string, rels []string) []PlacedFile {
	out := make([]PlacedFile, 0, len(rels))
	for _, rel := range rels {
		abs, err := ResolveUnder(root, rel)
		if err != nil {
			continue
		}
		info, err := os.Lstat(abs)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		out = append(out, PlacedFile{Rel: rel, Abs: abs})
	}
	return out
}
