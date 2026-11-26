package coder

import (
	"log"
	"slices"
	"strconv"

	"github.com/marein/dev-cockpit/internal/tmux"
)

// LateSessionRecords is the optional capability of a repository whose CLI
// writes a session's record only with the first message, under an id the CLI
// mints and tells nobody: until then there is nothing a promote could find.
// A session that comes up without a task therefore keeps the key the cockpit
// minted, and the one exact tie between its pane and the record that appears
// later is the process that writes it. ProcessSession answers the id of the
// stored session the process tree under pid writes, and every snapshot asks
// it for a pane that has no record under its own name.
type LateSessionRecords interface {
	ProcessSession(pid int) (string, bool)
}

// SessionNamer is the optional capability of a repository to write a name
// into a session's own record, for a CLI that takes no name on its command
// line. The cockpit names a fresh session through it once its record exists.
type SessionNamer interface {
	NameSession(sessionID, name string) error
}

// lateRecord finds the stored session of a pane that has none under its own
// name, see LateSessionRecords. The pane keeps its name, so the address a
// page was opened under keeps reaching it.
func lateRecord(p tmux.Pane, prov Coder, resumableByID map[string]Session) (Session, bool) {
	late, ok := prov.SessionRepository().(LateSessionRecords)
	if !ok {
		return Session{}, false
	}
	pid, err := strconv.Atoi(p.PID)
	if err != nil {
		return Session{}, false
	}
	id, ok := late.ProcessSession(pid)
	if !ok {
		return Session{}, false
	}
	match, ok := resumableByID[id]
	return match, ok
}

// lateTarget is the promote of a coder that ties a pane to its record through
// the process, see LateSessionRecords: the record the pane's own process
// writes, never a guess on the working directory, so two sessions started in
// one project at the same moment each land on their own.
func lateTarget(candidates []Session, seen map[string]bool, late LateSessionRecords, pid int) (Session, bool) {
	id, ok := late.ProcessSession(pid)
	if !ok || seen[id] {
		return Session{}, false
	}
	for _, r := range candidates {
		if r.SessionID == id {
			return r, true
		}
	}
	return Session{}, false
}

// nameSession writes the name a session was started with into its record,
// for a CLI that takes no name on its command line. Best effort: a session that keeps the CLI's own title is still the
// session that was asked for.
func (s *Manager) nameSession(sessionID, name string) {
	namer, ok := s.coder.SessionRepository().(SessionNamer)
	if !ok || name == "" {
		return
	}
	if err := namer.NameSession(sessionID, name); err != nil {
		log.Printf("coder %s: session %s keeps the name its CLI gave it: %v", s.coder.ID(), sessionID, err)
	}
}

// nameLateSessions names a session whose record appeared after its start,
// see LateSessionRecords, with the name it was started under. The name comes
// off the pane once the attempt is over, so it is written once and a rename
// made inside the coder afterwards stands. It runs beside the snapshot, the
// CLI call behind it takes its time.
func (s *Manager) nameLateSessions(panes []tmux.Pane, running []Running) {
	if _, ok := s.coder.SessionRepository().(SessionNamer); !ok {
		return
	}
	for i, r := range running {
		if r.TmuxSession == r.Identifier {
			continue
		}
		for _, p := range panes {
			if p.Name != r.TmuxSession || p.Coder != s.coder.ID() || p.CoderName == "" {
				continue
			}
			running[i].Name = p.CoderName
			if _, busy := s.naming.LoadOrStore(p.Name, true); busy {
				continue
			}
			go func(pane, id, name string) {
				defer s.naming.Delete(pane)
				s.nameSession(id, name)
				_ = s.tmux.SetOption(pane, coderNameOption, "")
				s.Invalidate()
			}(p.Name, r.Identifier, p.CoderName)
		}
	}
}

// StartedAs answers the running coder that was started under key and took
// the id of its record since, see LateSessionRecords. The key is the address
// a page opened before still holds, so every route of a coder answers to it.
func (s *Manager) StartedAs(key string) (Running, bool) {
	return startedAs(s.Snapshot().Running, key)
}

func startedAs(running []Running, key string) (Running, bool) {
	for _, r := range running {
		if r.TmuxSession == key && r.Identifier != key {
			return r, true
		}
	}
	return Running{}, false
}

// StartKeys answers, for the running coders among ids that took the id of
// their record since, the key each was started under, mapped to that id. A
// page opened before still holds the key, see StartedAs.
func (s *Manager) StartKeys(ids []string) map[string]string {
	return startKeys(s.Snapshot().Running, ids)
}

func startKeys(running []Running, ids []string) map[string]string {
	keys := map[string]string{}
	for _, r := range running {
		if r.TmuxSession != r.Identifier && slices.Contains(ids, r.Identifier) {
			keys[r.TmuxSession] = r.Identifier
		}
	}
	return keys
}

// movedPanes answers the running sessions whose pane was seen under another
// id on the tick before, and the panes of this tick.
func movedPanes(before map[string]string, running map[string]Running) ([]Running, map[string]string) {
	now := make(map[string]string, len(running))
	var moved []Running
	for id, r := range running {
		now[r.TmuxSession] = id
		if was, ok := before[r.TmuxSession]; ok && was != id {
			moved = append(moved, r)
		}
	}
	return moved, now
}

// OnMoved hands fn every session whose record appeared late, see
// LateSessionRecords, once its pane is tied to that record: from is the key
// the pane keeps, to the record's id, which its signals carry from then on
// and its resume comes back under. Whatever was arranged under the key
// follows it there, before any snapshot names the new id, so fn runs inside
// the snapshot and must not ask this manager anything.
func (s *Manager) OnMoved(fn func(from, to string)) {
	s.movesMu.Lock()
	s.moved = fn
	s.movesMu.Unlock()
}

func (s *Manager) announceMoves(running []Running) {
	if _, ok := s.coder.SessionRepository().(LateSessionRecords); !ok {
		return
	}
	s.movesMu.Lock()
	defer s.movesMu.Unlock()
	if s.moved == nil {
		return
	}
	var moved []Running
	moved, s.moves = lateMoves(s.moves, running)
	for _, r := range moved {
		s.moved(r.TmuxSession, r.Identifier)
	}
}

// lateMoves answers the running sessions whose id is not the name of their
// pane and which announced does not hold yet, and what is announced after
// them. Every process announces such a pane once, so a restart in between
// still moves what was arranged under the key.
func lateMoves(announced map[string]string, running []Running) ([]Running, map[string]string) {
	next := map[string]string{}
	var moved []Running
	for _, r := range running {
		if r.TmuxSession == r.Identifier {
			continue
		}
		next[r.TmuxSession] = r.Identifier
		if announced[r.TmuxSession] != r.Identifier {
			moved = append(moved, r)
		}
	}
	return moved, next
}
