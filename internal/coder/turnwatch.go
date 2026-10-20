package coder

import (
	"time"
)

// turnReadBudget is what one record reading of the watcher may cost. Only
// the Empty, Finished, InToolCall, AwaitingApproval and LastMessageAt
// answers are used, so the smallest budget that still parses the tail is
// enough.
const turnReadBudget = 200

// TurnWatch is what RunTurnWatch reports to and asks. OnSeen(id) fires once
// when a session enters the running set, which is where the tracker learns
// the coder's chosen policy; OnGone(id) when it leaves, so a mark cannot
// outlive its terminal. For a coder whose ActivityProfile chose WatchRecord,
// OnTurn reports the record's own account whenever it moved: whether a turn
// is open, whether it stands inside a tool call, and when its newest message
// was written as the word's own time (wordTime). RecordOf answers under which
// id the record of a session is kept now, the session's own id unless the
// coder moved it: claude starts a new transcript on /clear and /resume while
// the terminal stays.
//
// OnRenamed reports a session whose display name moved since the last tick.
// A coder names its own sessions, so a rename happens inside the CLI and
// reaches no handler that could announce it; the name is in the snapshot this
// loop reads anyway, so it costs one string compare. It is compared above the
// record section on purpose: a coder without WatchRecord leaves there, and its
// sessions get renamed like everybody else's.
type TurnWatch struct {
	OnSeen    func(id string, startedAt time.Time)
	OnTurn    func(id string, open, inTool bool, at time.Time)
	OnGone    func(id string)
	OnRenamed func(id, name, cwd string)
	RecordOf  func(id string) string
}

// RunTurnWatch follows every running session of this manager for the
// activity tracker, see TurnWatch.
//
// A stamp is taken per session per tick and the record is only read when the
// stamp moved, so an idle session costs one stat per tick. A session whose
// record cannot be stamped or read yet reports nothing at all: no account is
// exactly what the movement fallback is for. A record that holds no message
// yet (Activity.Empty) is the same case with a file: a coder that notes its
// boot into the record before anybody typed has no account to give, see
// openTurn. Blocks; run it in a goroutine.
func (s *Manager) RunTurnWatch(interval time.Duration, w TurnWatch) {
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
				w.OnGone(id)
			}
		}
		for id, r := range running {
			if _, ok := stamps[id]; !ok {
				stamps[id] = time.Time{}
				names[id] = r.Name
				w.OnSeen(id, r.StartedAt)
				continue
			}
			// Only a name that moved between two ticks is a rename. A session
			// seen for the first time above brought its name, and one that
			// left forgot it, so a start and a return announce nothing.
			if names[id] != r.Name {
				names[id] = r.Name
				w.OnRenamed(id, r.Name, r.CWD)
			}
		}
		if !watchRecord || stamper == nil || reporter == nil {
			continue
		}
		for id, r := range running {
			record := w.RecordOf(id)
			stamp, err := stamper.SessionActivityStamp(record)
			if err != nil {
				// No record yet; the movement fallback carries the session.
				continue
			}
			if stamp.Equal(stamps[id]) {
				continue
			}
			stamps[id] = stamp
			activity, err := reporter.SessionActivity(record, 1, turnReadBudget)
			if err != nil {
				continue
			}
			open, spoken := openTurn(activity, stamp, r.StartedAt)
			if !spoken {
				continue
			}
			w.OnTurn(id, open, activity.InToolCall, wordTime(activity, stamp))
		}
	}
}

// wordTime dates the record's word by its newest message, and by the file's
// stamp only for a reader that cannot date its messages. The stamp moves
// with bookkeeping, and a hook that fired after the message was written, a
// permission ask after the tool call it asks about, would otherwise be
// overruled by the same message read again under a fresher stamp.
func wordTime(activity Activity, stamp time.Time) time.Time {
	if activity.LastMessageAt.IsZero() {
		return stamp
	}
	return activity.LastMessageAt
}

// openTurn decides what the watcher reports as open, and whether it reports
// at all. A reading whose tail the coder's hooks speak for (Activity.Aside)
// is no word of the record's, and spoken is false for it. A reading without
// a single message (Activity.Empty) is no account of a turn either: claude
// 2.1.277 wrote the transcript at boot with a note about the instruction
// files it loaded, so the file exists and carries a stamp after the start
// while nobody has said a word, and judged by that stamp below it read as a
// prompt just received and pinned the working mark from the start until the
// cap or the first prompt. Reporting it as over instead would silence the
// movement shelf, which is the one reading left for a session nobody has
// talked to yet.
//
// A reading with messages answers for the record; what it cannot know is
// whether its newest message belongs to the terminal it is read for. A
// message older than the terminal was left by a previous life, a turn aborted without a written end, stopped
// and resumed later, and taking its word would pin the working mark until
// the backstop. Such a turn is reported as over, not skipped: over is what
// that turn is for this terminal, and the word also keeps the movement
// shelf from mistaking typing into the resumed session for work. A reader
// that cannot date its messages is judged by the record's stamp instead,
// which a serve restart over a live turn still passes: the pane survives
// the restart, so whatever the turn wrote was written in this life.
func openTurn(activity Activity, stamp, startedAt time.Time) (open, spoken bool) {
	if activity.Empty || activity.Aside {
		return false, false
	}
	wordAt := activity.LastMessageAt
	if wordAt.IsZero() {
		wordAt = stamp
	}
	return !activity.Finished && !activity.AwaitingApproval && !wordAt.Before(startedAt), true
}
