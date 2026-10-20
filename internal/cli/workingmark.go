package cli

import (
	"path/filepath"
	"time"

	"github.com/marein/dev-cockpit/internal/activity"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/web"
)

// newWorkingMarks builds the tracker behind the working marks with the
// account a previous process left, see Tracker.Persist. Every change goes
// out as one activity snapshot on the event bus, the shape the icons
// decorate from.
func newWorkingMarks(stateDir string, bus *eventbus.Bus) *activity.Tracker {
	tracker := activity.NewTracker()
	tracker.Persist(filepath.Join(stateDir, "working-marks.json"))
	tracker.SetOnChange(func(working map[string]int64) { bus.Publish(web.ActivityEvent(working)) })
	go tracker.Run(time.Second)
	return tracker
}

// wireWorkingMark lets the coders' own hooks drive the working mark: every
// hook the inbox ingests is read by the coder it came from, which alone knows
// what its names mean (coder.TurnSignaler), or by the inbox's own words for a
// coder without a reading of its own. The word reaches the tracker dated by
// the moment the hook fired, so it orders against the record's messages and
// the person's keys. A main thread hook also says under which id the coder
// records the terminal now, the one the record watcher reads.
func wireWorkingMark(notifier *notify.Service, tracker *activity.Tracker, readers map[string]coder.TurnSignaler) {
	notifier.SetHook(func(targetID string, ev notify.HookEvent) {
		read := coder.InboxTurnSignal
		if reader, ok := readers[ev.Coder]; ok {
			read = reader.TurnSignal
		}
		if ev.AgentID == "" {
			tracker.SetRecord(targetID, ev.Session, ev.At)
		}
		switch read(coder.HookEvent{Name: ev.Name, AgentID: ev.AgentID, NotificationType: ev.NotificationType, ToolName: ev.ToolName, Source: ev.Source, Trigger: ev.Trigger, At: ev.At}) {
		case coder.TurnSignalOpen:
			tracker.SetTurn(targetID, true, false, ev.At)
		case coder.TurnSignalTool:
			tracker.SetTurn(targetID, true, true, ev.At)
		case coder.TurnSignalOver:
			tracker.SetTurn(targetID, false, false, ev.At)
		case coder.TurnSignalWaiting:
			tracker.SetWaiting(targetID, ev.At)
		case coder.TurnSignalQuestion:
			tracker.SetQuestion(targetID, ev.At)
		case coder.TurnSignalAlive:
			tracker.Alive(targetID, ev.At)
		}
	})
}

// turnReaders collects the coders that read their own hooks, by the id their
// inbox is named after.
func turnReaders(coders []*coder.Manager) map[string]coder.TurnSignaler {
	readers := map[string]coder.TurnSignaler{}
	for _, m := range coders {
		if reader, ok := m.Coder().(coder.TurnSignaler); ok {
			readers[m.ID()] = reader
		}
	}
	return readers
}

// watchWorkingMarks starts the watches one coder feeds the working marks
// with. The turn watch follows each session's own record where the coder
// keeps it now and is the account the tracker trusts first; its gone reports
// are what drop a dead session's mark. The session watch delivers the raw
// screen output the fallback shelf reads, for a coder whose profile chose it,
// and the bell, for a coder that rings one at its turn's end: a bell has no
// time of its own and closes the turn as of now.
//
// The turn watch carries one thing that is not about working marks: it
// reads the snapshot every second, and the snapshot holds the session names
// the CLIs write themselves, so a rename inside a coder is handed to
// onRenamed from there.
func watchWorkingMarks(m *coder.Manager, tracker *activity.Tracker, onRenamed func(id, name, cwd string), bell func(targetID string)) {
	// A session entering the running set brings its coder's chosen policy
	// along, so the tracker never guesses what applies to it.
	profile := m.Coder().ActivityProfile()
	policy := activity.Policy{
		OpenTurnCap:        profile.OpenTurnCap,
		MovementStartGrace: profile.MovementStartGrace,
	}
	go m.RunTurnWatch(time.Second, coder.TurnWatch{
		OnSeen:    func(id string, startedAt time.Time) { tracker.Configure(id, policy, startedAt) },
		OnTurn:    tracker.SetTurn,
		OnGone:    tracker.Forget,
		OnRenamed: onRenamed,
		RecordOf:  tracker.Record,
	})
	var onOutput, onBell func(targetID string)
	if profile.WatchScreen {
		onOutput = tracker.Output
	}
	if bell != nil {
		onBell = func(targetID string) {
			bell(targetID)
			tracker.SetTurn(targetID, false, false, time.Now())
		}
	}
	if onOutput != nil || onBell != nil {
		go m.RunSessionWatch(3*time.Second, onOutput, onBell)
	}
}
