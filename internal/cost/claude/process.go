package claude

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Running implements cost.Source: a claude process whose arguments name the
// session still writes its transcript. Only claude itself counts, the tmux
// server and the cockpit's own commands name the session too. It reads
// /proc, a system without one waits for nothing.
func (s *Source) Running(session string) bool {
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		comm, err := os.ReadFile(filepath.Join("/proc", e.Name(), "comm"))
		if err != nil || strings.TrimSpace(string(comm)) != "claude" {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err == nil && slices.Contains(strings.Split(string(cmdline), "\x00"), session) {
			return true
		}
	}
	return false
}
