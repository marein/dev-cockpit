//go:build linux || darwin

package hostinfo

import "syscall"

// disk answers for the filesystem path sits on. Statfs is one syscall and
// counts blocks, never a walk of the tree: a project directory with a million
// files costs the same as an empty one.
func disk(path string) (total, free uint64, ok bool) {
	if path == "" {
		return 0, 0, false
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return 0, 0, false
	}
	// Bsize is signed on Linux and unsigned on macOS, hence the conversion.
	size := uint64(fs.Bsize)
	// Bavail is what this user may still take, Bfree would include the
	// reserve only root can touch.
	return fs.Blocks * size, fs.Bavail * size, fs.Blocks > 0
}
