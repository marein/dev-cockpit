//go:build !linux

package proctree

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

type psStrategy struct{}

func newStrategy() strategy { return psStrategy{} }

func (psStrategy) Descendants(rootPID int) []int {
	out, err := exec.Command("ps", "-axo", "pid=,ppid=").Output()
	if err != nil {
		return []int{rootPID}
	}
	children := map[int][]int{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		ppid, ppidErr := strconv.Atoi(fields[1])
		if pidErr != nil || ppidErr != nil {
			continue
		}
		children[ppid] = append(children[ppid], pid)
	}
	seen := map[int]bool{}
	var result []int
	pending := []int{rootPID}
	for len(pending) > 0 {
		pid := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		result = append(result, pid)
		pending = append(pending, children[pid]...)
	}
	return result
}

func (psStrategy) Cmdline(pid int) []string {
	out, err := exec.Command("ps", "-ww", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return nil
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return nil
	}
	return strings.Fields(line)
}

func (psStrategy) CWD(pid int) string {
	out, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-d", "cwd", "-Fn").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if path := strings.TrimPrefix(line, "n"); path != line && path != "" {
			if resolved, err := filepath.EvalSymlinks(path); err == nil {
				return resolved
			}
			return path
		}
	}
	return ""
}

func (psStrategy) Environ(pid int) map[string]string {
	out, err := exec.Command("ps", "-ww", "-E", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil
	}
	env := map[string]string{}
	for _, tok := range strings.Fields(string(out)) {
		if k, v, ok := strings.Cut(tok, "="); ok && isEnvName(k) {
			env[k] = v
		}
	}
	return env
}

func isEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z'):
		case i > 0 && c >= '0' && c <= '9':
		default:
			return false
		}
	}
	return true
}
