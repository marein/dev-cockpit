// Package proctree inspects process trees across supported platforms.
package proctree

// strategy provides one platform's access to the process table.
type strategy interface {
	// fill loads the whole table into t in a single shot when that is cheaper
	// than per-PID reads (platforms without /proc). It returns true once
	// children/cmdline/environ are populated; false leaves them to the lazy
	// per-PID readers below.
	fill(t *Tree) bool

	childrenOf(pid int) []int
	cmdlineOf(pid int) []string
	cwdOf(pid int) string
	environOf(pid int) map[string]string
}

var processTree strategy = newStrategy()

// Tree is a point-in-time view of the process table captured once per scan.
// Reuse it for every PID in that scan: on platforms without /proc this reads
// the table in one or two ps calls instead of reforking ps per PID, and every
// lookup is memoised so repeated queries (e.g. Descendants computed per pane,
// Environ checked per candidate) never refork.
type Tree struct {
	s        strategy
	bulk     bool // children/cmdline/environ already loaded by fill
	children map[int][]int
	cmdline  map[int][]string
	environ  map[int]map[string]string
	cwd      map[int]string
	haveCWD  map[int]bool
}

// Capture snapshots the current process table.
func Capture() *Tree {
	t := &Tree{
		s:        processTree,
		children: map[int][]int{},
		cmdline:  map[int][]string{},
		environ:  map[int]map[string]string{},
		cwd:      map[int]string{},
		haveCWD:  map[int]bool{},
	}
	t.bulk = t.s.fill(t)
	return t
}

// Descendants returns root and all its transitive child PIDs.
func (t *Tree) Descendants(rootPID int) []int {
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
		pending = append(pending, t.childrenOf(pid)...)
	}
	return result
}

// Cmdline returns the argv of the given process.
func (t *Tree) Cmdline(pid int) []string {
	if t.bulk {
		return t.cmdline[pid]
	}
	if v, ok := t.cmdline[pid]; ok {
		return v
	}
	v := t.s.cmdlineOf(pid)
	t.cmdline[pid] = v
	return v
}

// CWD returns the working directory of the given process with symlinks
// resolved. Resolved lazily and memoised: only the rare legacy/orphan paths
// need it, so each PID is looked up at most once (and on platforms without
// /proc that lookup forks lsof, which the common ID-matched path avoids).
func (t *Tree) CWD(pid int) string {
	if t.haveCWD[pid] {
		return t.cwd[pid]
	}
	v := t.s.cwdOf(pid)
	t.cwd[pid] = v
	t.haveCWD[pid] = true
	return v
}

// Environ returns the environment of the given process.
func (t *Tree) Environ(pid int) map[string]string {
	if t.bulk {
		return t.environ[pid]
	}
	if v, ok := t.environ[pid]; ok {
		return v
	}
	v := t.s.environOf(pid)
	t.environ[pid] = v
	return v
}

func (t *Tree) childrenOf(pid int) []int {
	if t.bulk {
		return t.children[pid]
	}
	if v, ok := t.children[pid]; ok {
		return v
	}
	v := t.s.childrenOf(pid)
	t.children[pid] = v
	return v
}
