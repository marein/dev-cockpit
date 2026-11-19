package sshagent

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/marein/dev-cockpit/internal/proctree"
)

// remoteCommand is the command git hands ssh to run on the server, the
// service with the quoted repository path, at the end of the line.
var remoteCommand = regexp.MustCompile(`(?:^| )git-(receive|upload)-pack '.*'$`)

// callers reads the processes connected to a kind's socket, in the order
// they connected, each once.
func (s *Service) callers(k Kind) []Caller {
	type joined struct {
		pid int
		seq uint64
	}
	var conns []joined
	s.mu.Lock()
	for c, seq := range s.kinds[k].callers {
		if c.pid > 0 {
			conns = append(conns, joined{c.pid, seq})
		}
	}
	s.mu.Unlock()
	sort.Slice(conns, func(i, j int) bool { return conns[i].seq < conns[j].seq })
	tree := proctree.Capture()
	seen := map[int]bool{}
	var out []Caller
	for _, j := range conns {
		pid, caller := callerOf(tree, j.pid)
		if seen[pid] {
			continue
		}
		seen[pid] = true
		if len(caller.Command) > 0 {
			out = append(out, caller)
		}
	}
	return out
}

// callerOf reads the process that connected as the person knows it: the
// topmost git process above it, so a pull reads as the pull and not as the
// fetch it runs, and ssh's line where no git runs above it. It answers the
// process it read along.
func callerOf(tree *proctree.Tree, pid int) (int, Caller) {
	chain := append([]int{pid}, tree.Ancestors(pid)...)
	top := 0
	for _, p := range chain[1:] {
		if isGit(tree.Cmdline(p)) {
			top = p
		}
	}
	if top > 0 {
		return top, Caller{Command: tree.Cmdline(top), Dir: tree.CWD(top), Chain: chain}
	}
	command := tree.Cmdline(pid)
	return pid, Caller{Command: command, Dir: tree.CWD(pid), Transfer: transferOf(command), Chain: chain}
}

func isGit(argv []string) bool {
	return len(argv) > 0 && filepath.Base(argv[0]) == "git"
}

// transferOf reads the transfer off an ssh line by the service git asks the
// server for. The line is joined first, where it comes from ps an argument
// that holds a space arrives in pieces.
func transferOf(argv []string) Transfer {
	if len(argv) < 2 || filepath.Base(argv[0]) != "ssh" {
		return ""
	}
	m := remoteCommand.FindStringSubmatch(strings.Join(argv[1:], " "))
	switch {
	case m == nil:
		return ""
	case m[1] == "receive":
		return TransferPush
	}
	return TransferFetch
}
