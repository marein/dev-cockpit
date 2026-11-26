//go:build linux

package proctree

import (
	"fmt"
	"os"
	"path/filepath"
)

// OpenFiles answers the paths a process holds open, empty for a process that
// is gone or belongs to somebody else.
func OpenFiles(pid int) []string {
	dir := fmt.Sprintf("/proc/%d/fd", pid)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if target, err := os.Readlink(filepath.Join(dir, entry.Name())); err == nil {
			out = append(out, target)
		}
	}
	return out
}
