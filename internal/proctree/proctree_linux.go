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

func (procStrategy) Descendants(rootPID int) []int {
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
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
		if err != nil {
			continue
		}
		for _, f := range strings.Fields(string(data)) {
			if c, err := strconv.Atoi(f); err == nil {
				pending = append(pending, c)
			}
		}
	}
	return result
}

func (procStrategy) Cmdline(pid int) []string {
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

func (procStrategy) CWD(pid int) string {
	link, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(link); err == nil {
		return resolved
	}
	return link
}

func (procStrategy) Environ(pid int) map[string]string {
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
