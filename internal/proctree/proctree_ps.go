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

// fill reads the whole process table in two ps calls: one for the parent links
// and argv, one for the environment. Without /proc every per-PID query forks ps,
// so a scan over a process tree of N descendants would otherwise spawn O(N)
// processes; this caps the table reads at two forks total. CWD stays lazy (it
// forks lsof) because the common ID-matched path never needs it.
func (psStrategy) fill(t *Tree) bool {
	children, cmdline, ok := psTable()
	if !ok {
		return false
	}
	environ, ok := psEnviron()
	if !ok {
		return false
	}
	t.children = children
	t.cmdline = cmdline
	t.environ = environ
	return true
}

// psTable returns pid->children and pid->argv from a single `ps` invocation.
func psTable() (children map[int][]int, cmdline map[int][]string, ok bool) {
	out, err := exec.Command("ps", "-axww", "-o", "pid=,ppid=,command=").Output()
	if err != nil {
		return nil, nil, false
	}
	children = map[int][]int{}
	cmdline = map[int][]string{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		ppid, ppidErr := strconv.Atoi(fields[1])
		if pidErr != nil || ppidErr != nil {
			continue
		}
		children[ppid] = append(children[ppid], pid)
		if len(fields) > 2 {
			cmdline[pid] = fields[2:]
		}
	}
	return children, cmdline, true
}

// psEnviron returns pid->environment from a single `ps -E` invocation.
func psEnviron() (map[int]map[string]string, bool) {
	out, err := exec.Command("ps", "-axwwE", "-o", "pid=,command=").Output()
	if err != nil {
		return nil, false
	}
	environ := map[int]map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		environ[pid] = parseEnviron(fields[1:])
	}
	return environ, true
}

func parseEnviron(tokens []string) map[string]string {
	env := map[string]string{}
	for _, tok := range tokens {
		if k, v, ok := strings.Cut(tok, "="); ok && isEnvName(k) {
			env[k] = v
		}
	}
	return env
}

func (psStrategy) childrenOf(pid int) []int {
	children, _, ok := psTable()
	if !ok {
		return nil
	}
	return children[pid]
}

func (psStrategy) cmdlineOf(pid int) []string {
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

func (psStrategy) cwdOf(pid int) string {
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

func (psStrategy) environOf(pid int) map[string]string {
	out, err := exec.Command("ps", "-ww", "-E", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil
	}
	return parseEnviron(strings.Fields(string(out)))
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
