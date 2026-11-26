//go:build !linux

package proctree

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// OpenFiles answers the paths a process holds open, empty for a process that
// is gone or belongs to somebody else.
func OpenFiles(pid int) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "lsof", "-a", "-p", strconv.Itoa(pid), "-Fn").Output()
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if path := strings.TrimPrefix(line, "n"); path != line && path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}
