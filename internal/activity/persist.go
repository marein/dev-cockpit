package activity

import (
	"log"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// savedSession is one session's account as the state file keeps it: the
// newest word with when it was written, whether it was the tracker's own
// guess, when the account was last heard, and where the coder records the
// session now. Movement and pending keys are not kept, they belong to the
// moment.
type savedSession struct {
	Turn     string    `json:"turn"`
	At       time.Time `json:"at"`
	Guessed  bool      `json:"guessed,omitempty"`
	HeardAt  time.Time `json:"heardAt"`
	Record   string    `json:"record,omitempty"`
	RecordAt time.Time `json:"recordAt,omitzero"`
}

var turnNames = map[turnState]string{
	turnOpen:       "open",
	turnOpenInTool: "tool",
	turnOver:       "over",
	turnWaiting:    "waiting",
	turnQuestion:   "question",
}

// Persist names the state file the account is kept in and reads back what a
// previous process left there, before any feeder starts. A session keeps its
// last word across a serve restart: a turn that stays open in a pane that
// outlived the process keeps its mark, one that waits keeps it down, and a
// session that came up idle stays known as idle. The words written while the
// server was down, the hooks still in the inbox and the record read again,
// are ordered against the kept one by their own time like any other word.
// A session whose terminal ended meanwhile is never claimed by a watcher and
// leaves with the sweep, see sweepKeep.
func (t *Tracker) Persist(path string) {
	saved := map[string]savedSession{}
	if err := statefile.Read(path, &saved); err != nil {
		log.Printf("activity: %v", err)
	}
	t.mu.Lock()
	t.path = path
	for id, kept := range saved {
		state, ok := turnByName(kept.Turn)
		if !ok && kept.Record == "" {
			continue
		}
		s := t.session(id)
		s.turn, s.turnAt, s.guessed = state, kept.At, kept.Guessed
		if state == turnWaiting {
			s.askAt = kept.At
		}
		s.heardAt = kept.HeardAt
		if s.heardAt.Before(kept.At) {
			s.heardAt = kept.At
		}
		s.record, s.recordAt = kept.Record, kept.RecordAt
	}
	t.reconcile()
}

// save writes the account of every session to the state file. The caller
// holds the lock.
func (t *Tracker) save() {
	t.changed, t.heard = false, false
	if t.path == "" {
		return
	}
	out := map[string]savedSession{}
	for id, s := range t.sessions {
		name, known := turnNames[s.turn]
		if !known && s.record == "" {
			continue
		}
		out[id] = savedSession{Turn: name, At: s.turnAt, Guessed: s.guessed, HeardAt: s.heardAt, Record: s.record, RecordAt: s.recordAt}
	}
	statefile.Save(t.path, 0o600, out)
}

func turnByName(name string) (turnState, bool) {
	for state, n := range turnNames {
		if n == name {
			return state, true
		}
	}
	return turnUnknown, false
}
