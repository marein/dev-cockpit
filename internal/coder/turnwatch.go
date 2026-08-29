package coder

import (
	"time"
)

// turnReadBudget is what one record reading of the watcher may cost. Only
// the Finished, InToolCall, AwaitingApproval and LastMessageAt answers are
// used, so the smallest budget that still parses the tail is enough.
const turnReadBudget = 200

// RunTurnWatch follows every running session of this manager for the
// activity tracker. onSeen(id) fires once when a session enters the running
// set, which is where the tracker learns the coder's chosen policy;
// onGone(id) when it leaves, so a mark cannot outlive its terminal. For a
// coder whose ActivityProfile chose WatchRecord, onTurn reports the record's
// own account whenever it moved: whether a turn is open, whether it stands
// inside a tool call, and the record's stamp as the word's own time.
//
// onRenamed reports a session whose display name moved since the last tick.
// A coder names its own sessions, so a rename happens inside the CLI and
// reaches no handler that could announce it; the name is in the snapshot this
// loop reads anyway, so it costs one string compare. It is compared above the
// record section on purpose: a coder without WatchRecord leaves there, and its
// sessions get renamed like everybody else's.
//
// A stamp is taken per session per tick and the record is only read when the
// stamp moved, so an idle session costs one stat per tick. A session whose
// record cannot be stamped or read yet reports nothing at all: no account is
// exactly what the movement fallback is for. Blocks; run it in a goroutine.
func (s *Manager) RunTurnWatch(interval time.Duration, onSeen func(id string, startedAt time.Time), onTurn func(id string, open, inTool bool, at time.Time), onGone func(id string), onRenamed func(id, name, cwd string)) {
	watchRecord := s.coder.ActivityProfile().WatchRecord
	stamper, _ := s.coder.(ActivityStamper)
	reporter, _ := s.coder.(ActivityReporter)
	stamps := map[string]time.Time{}
	names := map[string]string{}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		running := map[string]Running{}
		for _, r := range s.Snapshot().Running {
			running[r.Identifier] = r
		}
		for id := range stamps {
			if _, ok := running[id]; !ok {
				delete(stamps, id)
				delete(names, id)
				onGone(id)
			}
		}
		for id, r := range running {
			if _, ok := stamps[id]; !ok {
				stamps[id] = time.Time{}
				names[id] = r.Name
				onSeen(id, r.StartedAt)
				continue
			}
			// Only a name that moved between two ticks is a rename. A session
			// seen for the first time above brought its name, and one that
			// left forgot it, so a start and a return announce nothing.
			if names[id] != r.Name {
				names[id] = r.Name
				onRenamed(id, r.Name, r.CWD)
			}
		}
		if !watchRecord || stamper == nil || reporter == nil {
			continue
		}
		for id, r := range running {
			stamp, err := stamper.SessionActivityStamp(id)
			if err != nil {
				// No record yet; the movement fallback carries the session.
				continue
			}
			if stamp.Equal(stamps[id]) {
				continue
			}
			stamps[id] = stamp
			activity, err := reporter.SessionActivity(id, 1, turnReadBudget)
			if err != nil {
				continue
			}
			onTurn(id, openTurn(activity, stamp, r.StartedAt), activity.InToolCall, stamp)
		}
	}
}

// openTurn decides what the watcher reports as open. The reading answers
// for the record; what it cannot know is whether its newest message belongs
// to the terminal it is read for. A message older than the terminal was
// left by a previous life, a turn aborted without a written end, stopped
// and resumed later, and taking its word would pin the working mark until
// the backstop. Such a turn is reported as over, not skipped: over is what
// that turn is for this terminal, and the word also keeps the movement
// shelf from mistaking typing into the resumed session for work. A reader
// that cannot date its messages is judged by the record's stamp instead,
// which a serve restart over a live turn still passes: the pane survives
// the restart, so whatever the turn wrote was written in this life.
func openTurn(activity Activity, stamp, startedAt time.Time) bool {
	wordAt := activity.LastMessageAt
	if wordAt.IsZero() {
		wordAt = stamp
	}
	return !activity.Finished && !activity.AwaitingApproval && !wordAt.Before(startedAt)
}
