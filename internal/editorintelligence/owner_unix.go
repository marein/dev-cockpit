//go:build unix

package editorintelligence

import (
	"io/fs"
	"syscall"
)

// fileUID answers the owning user id of a file, the half of a stat the
// portable FileInfo does not carry.
func fileUID(info fs.FileInfo) (int, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Uid), true
}
