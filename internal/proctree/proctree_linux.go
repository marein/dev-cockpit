//go:build linux

package proctree

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type procStrategy struct{}

func newStrategy() strategy { return procStrategy{} }

// fill is a no-op on Linux: /proc reads are syscall-cheap, so the Tree reads
// them lazily and memoises per PID rather than slurping the whole table.
func (procStrategy) fill(*Tree) bool { return false }

func (procStrategy) childrenOf(pid int) []int {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
	if err != nil {
		return nil
	}
	var out []int
	for _, f := range strings.Fields(string(data)) {
		if c, err := strconv.Atoi(f); err == nil {
			out = append(out, c)
		}
	}
	return out
}

// parentOf reads the parent from /proc/<pid>/stat. The name in parentheses
// before it may hold spaces and parentheses of its own, so the fields are
// counted from the last closing one.
func (procStrategy) parentOf(pid int) int {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0
	}
	i := bytes.LastIndexByte(data, ')')
	if i < 0 {
		return 0
	}
	fields := strings.Fields(string(data[i+1:]))
	if len(fields) < 2 {
		return 0
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0
	}
	return ppid
}

func (procStrategy) cmdlineOf(pid int) []string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return nil
	}
	var out []string
	for _, p := range bytes.Split(data, []byte{0}) {
		if len(p) > 0 {
			out = append(out, string(p))
		}
	}
	return out
}

func (procStrategy) cwdOf(pid int) string {
	link, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(link); err == nil {
		return resolved
	}
	return link
}

func (procStrategy) environOf(pid int) map[string]string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, kv := range bytes.Split(data, []byte{0}) {
		if len(kv) == 0 {
			continue
		}
		if k, v, ok := bytes.Cut(kv, []byte{'='}); ok {
			out[string(k)] = string(v)
		}
	}
	return out
}
