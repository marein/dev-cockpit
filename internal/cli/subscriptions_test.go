package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// The list prints what the page's row says: the event and where it applies,
// the task, the state with the one shot mark, and the bounds. An absent field
// prints as nothing, never as a question mark.
func TestSubscriptionListPrintsARowWhole(t *testing.T) {
	var out bytes.Buffer
	printSubscription(&out, map[string]any{
		"id": "944456837e6ec2fd", "label": "Job closed", "where": "any job of mine",
		"task": "Hand out the next step", "state": "standing", "once": true, "fired": float64(2),
		"nextAt": "", "expiresAt": "2026-09-19T03:34:00Z", "maxPerHour": float64(6), "batchSeconds": float64(30),
		"note": "Fired for Job done: readme-task.", "ownerName": "Release work",
	}, true, time.Now())
	text := out.String()
	for _, want := range []string{
		"944456837e6ec2fd  Job closed, any job of mine  (Release work)",
		"task      Hand out the next step",
		"state     standing, once, fired 2 times",
		"6 turns per hour at most", "batch 30s", "until 2026-09-19",
		"last      Fired for Job done: readme-task.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the list does not say %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "?") {
		t.Fatalf("an absent field prints as nothing, got:\n%s", text)
	}
	out.Reset()
	printSubscription(&out, map[string]any{
		"id": "b3eb709185e0d00c", "label": "Schedule", "where": "0 9 * * 1-5", "task": "morning", "state": "expired",
		"fired": float64(1), "nextAt": "2026-09-21T09:00:00Z", "expiresAt": "", "maxPerHour": float64(2),
	}, false, time.Now())
	text = out.String()
	for _, want := range []string{"Schedule, 0 9 * * 1-5", "state     expired, fired 1 time\n", "next 2026-09-21", "no expiry"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the list does not say %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "last") || strings.Contains(text, "(") {
		t.Fatalf("no note and no owner print nothing, got:\n%s", text)
	}
}
