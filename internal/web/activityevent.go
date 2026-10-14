package web

import (
	"maps"
	"slices"

	"github.com/marein/dev-cockpit/internal/eventbus"
)

// ActivityEvent is the working set as the event bus carries it, published on
// every change and sent as the snapshot of every connect: since is the moment
// each working session's mark went up, the map activity.Tracker.Working
// answers, and what the icons decorate from.
func ActivityEvent(working map[string]int64) eventbus.Event {
	return eventbus.Event{Type: "activity", Data: map[string]any{
		// TODO(v2.0.0): drop targets, the same sessions as a sorted list. A
		// page loaded before since existed reads only that, and only the tab
		// that ran an update reloads, every other one keeps it until its next
		// navigation.
		"targets": slices.Sorted(maps.Keys(working)),
		"since":   working,
	}}
}
