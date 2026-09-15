package web

import "github.com/marein/dev-cockpit/internal/notify"

// The Terminals button of the rail and the tabbar says that a terminal has
// news, and a terminal is a coder or a shell. What only runs in one is not one
// and has no business in that mark: a compose action, a backup job, a standing
// git question, and the assistants, which carry their own mark on their own
// button and get their own narrowing below. The bell is the one surface that
// still counts all of it.
//
// The test is positive and asked of the session lists rather than of the id's
// shape: a target kind nobody has thought of yet does not count until a coder
// or a shell answers to it. Both lists are the cached ones every page render
// already reads, so a burst of events costs no rescan, and the coder snapshots
// are the visible ones, which is what keeps the assistant's own conversations
// out without naming them here.
//
// A session that was deleted while its news stood therefore drops out too, and
// that is right: nothing in the terminals area shows it any more. The entry
// stays in the bell.

// terminalTargets keeps the targets a coder or a shell answers to, in the
// order they came in.
func (s *Server) terminalTargets(ids []string) []string {
	out := []string{}
	if len(ids) == 0 {
		return out
	}
	known := s.knownTerminals(ids)
	for _, id := range ids {
		if known[id] {
			out = append(out, id)
			delete(known, id)
		}
	}
	return out
}

// anyTerminalNews reports whether a coder or a shell holds unread news, the
// one question the Terminals button asks on a server-side render.
func (s *Server) anyTerminalNews() bool {
	unread := s.notifier.UnreadTargets()
	ids := make([]string, 0, len(unread))
	for id := range unread {
		ids = append(ids, id)
	}
	return len(s.terminalTargets(ids)) > 0
}

// knownTerminals answers which of the wanted ids a coder or a shell answers
// to. Stopped coders count: they keep their news in the chips and in the
// resume lists, which is terminals area all the same.
func (s *Server) knownTerminals(want []string) map[string]bool {
	wanted := make(map[string]bool, len(want))
	for _, id := range want {
		wanted[id] = true
	}
	known := map[string]bool{}
	for _, m := range s.coders {
		snap := m.Snapshot()
		for _, running := range snap.Running {
			if wanted[running.Identifier] {
				known[running.Identifier] = true
			}
		}
		for _, stored := range snap.Resumable {
			if wanted[stored.SessionID] {
				known[stored.SessionID] = true
			}
		}
	}
	if s.shells != nil {
		for _, sh := range s.shells.List() {
			if wanted[sh.Identifier] {
				known[sh.Identifier] = true
			}
		}
	}
	return known
}

// notifyPayload is the notification event as the browser receives it: the
// service's own fields plus the terminal subset. The split lives here and not
// in internal/notify because that package carries targets and classifies none
// of them, see its package comment.
type notifyPayload struct {
	notify.Event
	// Terminals is Targets narrowed to the coders and the shells, what the
	// Terminals news dot reads. Targets itself stays whole, the bell counts
	// from it.
	Terminals []string `json:"terminals"`
	// Assistants is the same narrowing for the assistant area, and it needs one
	// of its own for the same reason: its entry points carry one dot for the
	// whole area, so they ask whether any assistant is new, not whether a
	// particular one is. Naming a single assistant on the mark is what made it
	// stay dark for news in every other one.
	Assistants []string `json:"assistants"`
}

func (s *Server) notifyPayload(ev notify.Event) notifyPayload {
	return notifyPayload{
		Event:      ev,
		Terminals:  s.terminalTargets(ev.Targets),
		Assistants: s.assistantTargets(ev.Targets),
	}
}

// assistantTargets keeps the targets an assistant answers to, in the order they
// came in. It asks the index, the same list every render of the area reads, so
// an assistant that was deleted while its news stood drops out of the mark and
// keeps its entry in the bell, exactly like a deleted terminal does.
func (s *Server) assistantTargets(ids []string) []string {
	out := []string{}
	if len(ids) == 0 || s.assistants == nil {
		return out
	}
	known := map[string]bool{}
	for _, entry := range s.assistants.List() {
		known[entry.ID] = true
	}
	for _, id := range ids {
		if known[id] {
			out = append(out, id)
			delete(known, id)
		}
	}
	return out
}
