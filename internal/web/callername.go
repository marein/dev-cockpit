package web

import (
	"os"
	"time"

	"github.com/marein/dev-cockpit/internal/proctree"
	"github.com/marein/dev-cockpit/internal/tmux"
)

// callerNameWait bounds how long a question waits for the names of its
// callers. A lookup that takes longer leaves them out, and the question reads
// as it would without them.
const callerNameWait = 250 * time.Millisecond

// callerNames names who each chain of processes runs for, a chain being a
// process with its ancestors, nearest first. A name is empty where nothing was
// found or the lookup outlasted callerNameWait. It is display only.
func (s *Server) callerNames(chains [][]int) []string {
	return within(len(chains), func() []string { return s.nameChains(chains) })
}

// processName is callerNames for a process whose ancestors are read here.
func (s *Server) processName(pid int) string {
	return within(1, func() []string {
		return s.nameChains([][]int{append([]int{pid}, proctree.Capture().Ancestors(pid)...)})
	})[0]
}

func within(n int, lookup func() []string) []string {
	found := make(chan []string, 1)
	go func() { found <- lookup() }()
	timer := time.NewTimer(callerNameWait)
	defer timer.Stop()
	select {
	case names := <-found:
		return names
	case <-timer.C:
		return make([]string, n)
	}
}

// nameChains names the terminal or the assistant each chain runs in, from the
// first process of it the cockpit knows, see anchorOf.
func (s *Server) nameChains(chains [][]int) []string {
	var turns map[int]string
	if s.assistants != nil {
		turns = s.assistants.TurnProcesses()
	}
	var panes map[int]string
	read := false
	listed := func() map[int]string {
		if !read {
			panes, read = tmux.New().PaneSessions(), true
		}
		return panes
	}
	names := make([]string, len(chains))
	for i, chain := range chains {
		switch turn, session := anchorOf(chain, os.Getpid(), turns, listed); {
		case turn != "":
			names[i] = s.assistantLabel(turn)
		case session != "":
			names[i] = s.terminalName(session)
		}
	}
	return names
}

// anchorOf walks a chain up to the first process the cockpit started for a
// turn or a pane of a tmux session, and answers that turn's assistant or that
// session. The walk ends at the cockpit's own process: what the cockpit runs
// itself, like a git action of the UI, runs for nobody above it.
func anchorOf(chain []int, own int, turns map[int]string, panes func() map[int]string) (turn, session string) {
	for _, pid := range chain {
		if pid <= 0 || pid == own {
			return "", ""
		}
		if id, ok := turns[pid]; ok {
			return id, ""
		}
		if name, ok := panes()[pid]; ok {
			return "", name
		}
	}
	return "", ""
}

// terminalName is what a question calls the coder or shell of a tmux session:
// its name and the project it works in.
func (s *Server) terminalName(session string) string {
	for _, m := range s.coders {
		for _, r := range m.Snapshot().Running {
			if r.TmuxSession == session {
				return s.inProject(r.Name, r.CWD)
			}
		}
	}
	if s.shells != nil {
		for _, sh := range s.shells.List() {
			if sh.TmuxSession == session {
				return s.inProject(sh.Name, sh.CWD)
			}
		}
	}
	return ""
}

func (s *Server) inProject(name, dir string) string {
	if s.projects == nil {
		return name
	}
	if project := s.projects.ProjectNameFor(dir); project != "" {
		return name + " in " + project
	}
	return name
}
