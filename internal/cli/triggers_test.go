package cli

import (
	"strings"
	"testing"
	"time"
)

// The list prints what the page's row says: the event and where it applies,
// the task, the state with the one shot mark, and the bounds. An absent field
// prints as nothing, never as a question mark.
func TestTriggerListPrintsARowWhole(t *testing.T) {
	var out strings.Builder
	printTrigger(&out, map[string]any{
		"id": "944456837e6ec2fd", "label": "Job closed", "where": "any job of mine",
		"task": "Hand out the next step", "state": "standing", "open": true, "once": true, "fired": float64(2),
		"nextAt": "", "expiresAt": "2026-09-19T03:34:00Z", "batchSeconds": float64(30),
		"note": "Fired for Job done: readme-task.", "ownerName": "Release work",
	}, triggerReport{Owners: true})
	text := out.String()
	for _, want := range []string{
		"944456837e6ec2fd  Job closed, any job of mine  (Release work)",
		"task      Hand out the next step",
		"state     standing, once, fired 2 times",
		"batch 30s", "until 2026-09-19",
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
	printTrigger(&out, map[string]any{
		"id": "b3eb709185e0d00c", "label": "Schedule", "where": "0 9 * * 1-5", "task": "morning", "state": "standing",
		"open": true, "fired": float64(1), "nextAt": "2026-09-21T09:00:00Z", "expiresAt": "",
	}, triggerReport{})
	text = out.String()
	for _, want := range []string{"Schedule, 0 9 * * 1-5", "state     standing, fired 1 time\n", "next 2026-09-21", "no expiry"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the list does not say %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "last") || strings.Contains(text, "(") {
		t.Fatalf("no note and no owner print nothing, got:\n%s", text)
	}
	// A name is the head and the event moves one line under it, the way it
	// moves under the name in the row on the page: a crontab line says when a
	// schedule fires and never what for.
	out.Reset()
	printTrigger(&out, map[string]any{
		"id": "6a2f1d9c4b8e0175", "name": "Nightly summary", "label": "Schedule", "where": "0 4 * * *",
		"task": "summarise the day", "state": "standing", "open": true, "fired": float64(0),
	}, triggerReport{})
	text = out.String()
	for _, want := range []string{
		"6a2f1d9c4b8e0175  Nightly summary\n",
		"event     Schedule, 0 4 * * *",
		"task      summarise the day",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the list does not say %q:\n%s", want, text)
		}
	}
}

// The task is the bulk of a trigger list, so it is cut the way a job's
// criterion is cut, and the cut says how much is missing and which flag brings
// the rest. `--full` prints it whole.
func TestTriggerListCutsTheTaskAndSaysSo(t *testing.T) {
	task := strings.TrimSpace(strings.Repeat("hand the next step to a coder and report back, ", 12))
	row := map[string]any{
		"id": "944456837e6ec2fd", "label": "Job closed", "task": task,
		"state": "standing", "open": true, "fired": float64(1),
	}

	var cut strings.Builder
	printTrigger(&cut, row, triggerReport{})
	if strings.Contains(cut.String(), task) {
		t.Fatalf("a task of %d runes has to be cut in the list:\n%s", len([]rune(task)), cut.String())
	}
	for _, want := range []string{
		"task      " + strings.TrimSpace(string([]rune(task)[:maxJobNoteRunes])) + "…",
		"--full shows the tasks whole",
	} {
		if !strings.Contains(cut.String(), want) {
			t.Fatalf("the cut has to say %q:\n%s", want, cut.String())
		}
	}

	var whole strings.Builder
	printTrigger(&whole, row, triggerReport{Filter: triggerFilter{Full: true}})
	if !strings.Contains(whole.String(), "task      "+task) {
		t.Fatalf("--full has to print the task word for word:\n%s", whole.String())
	}
}

// A spent trigger fires nothing again, so it stands the way a closed job
// stands in `job-list`: its state and what became of it, without the task and
// the bounds, which describe work nobody can move. `--full` gives both back,
// so a `--contains` hit that fell in a task can be read where it was found.
func TestASpentTriggerStandsShortAndFullGivesItBack(t *testing.T) {
	row := map[string]any{
		"id": "b3eb709185e0d00c", "label": "Schedule", "where": "0 9 * * 1-5", "task": "summarise the day",
		"state": "expired", "fired": float64(1), "nextAt": "2026-09-21T09:00:00Z", "batchSeconds": float64(30),
		"note": "Answered for Schedule.",
	}

	var short strings.Builder
	printTrigger(&short, row, triggerReport{})
	for _, want := range []string{
		"b3eb709185e0d00c  Schedule, 0 9 * * 1-5\n",
		"state     expired, fired 1 time\n",
		"last      Answered for Schedule.",
	} {
		if !strings.Contains(short.String(), want) {
			t.Fatalf("a spent trigger does not say %q:\n%s", want, short.String())
		}
	}
	for _, gone := range []string{"task ", "bounds ", "summarise the day", "no expiry", "batch 30s"} {
		if strings.Contains(short.String(), gone) {
			t.Fatalf("a spent trigger must not print %q:\n%s", gone, short.String())
		}
	}

	var whole strings.Builder
	printTrigger(&whole, row, triggerReport{Filter: triggerFilter{Full: true}})
	for _, want := range []string{"task      summarise the day", "bounds    next 2026-09-21"} {
		if !strings.Contains(whole.String(), want) {
			t.Fatalf("--full does not give a spent trigger %q back:\n%s", want, whole.String())
		}
	}
}

// The listing stands in the two groups `job-list` and `status` stand in: every
// standing trigger, then the spent tail the server capped, with a line naming
// what the cap held back and what searches past it. A word that narrowed the
// list says so on the first line, with the count of what it left out.
func TestTriggerListSplitsStandingFromSpentAndNamesWhatIsLeftOut(t *testing.T) {
	report := triggerReport{
		Now:   time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC),
		Older: 6,
		Rows: []map[string]any{
			{"id": "aaa", "label": "Job done", "task": "hand out the next step", "state": "standing", "open": true, "fired": float64(0)},
			{"id": "bbb", "label": "Schedule", "task": "summarise the day", "state": "done", "fired": float64(1)},
		},
	}

	out := formatTriggers(report)
	for _, want := range []string{
		"Triggers at 2026-03-04 09:30\n",
		"\nStanding (1)\naaa  Job done\n",
		"\nSpent (7)\nbbb  Schedule\n",
		"and 6 older, --contains and --since search past this, --all lists every one",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the listing is missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "aaa  Job done") > strings.Index(out, "bbb  Schedule") {
		t.Fatalf("want the standing trigger first:\n%s", out)
	}

	narrowed := formatTriggers(triggerReport{Now: report.Now, Filter: triggerFilter{Contains: "morning"}, Dropped: 4})
	for _, want := range []string{
		`containing "morning" (4 other triggers are not shown)`,
		"nothing is waiting for an event", "Spent (0)", "none",
	} {
		if !strings.Contains(narrowed, want) {
			t.Fatalf("a narrowed listing is missing %q:\n%s", want, narrowed)
		}
	}
}

// `--since` is the second way past the cap, the one "what happened yesterday"
// asks for. It reads what `job-list --since` reads, through the same parser,
// and the moment is what travels to the server, so a span or a date is refused
// where it was typed and the header says what was narrowed and what it left
// out.
func TestTriggerListNarrowsByWhatChangedSince(t *testing.T) {
	now := time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC)
	ready, err := triggerFilter{}.parse("24h", now)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if want := now.Add(-24 * time.Hour); !ready.Since.Equal(want) {
		t.Fatalf("a span back from now is %s, want %s", ready.Since, want)
	}
	if _, err := (triggerFilter{}).parse("yesterday", now); err == nil {
		t.Fatalf("a word that is neither a span nor a date has to be refused")
	}
	out := formatTriggers(triggerReport{Now: now, Filter: ready, Dropped: 3})
	want := ", changed since " + ready.Since.Local().Format("2006-01-02 15:04") + " (3 other triggers are not shown)"
	if !strings.Contains(out, want) {
		t.Fatalf("the header does not say %q:\n%s", want, out)
	}
	// Both filters at once read as one list of what was narrowed, the way
	// `job-list` writes them.
	both, err := triggerFilter{Contains: "morning"}.parse("2026-03-03", now)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if line := triggerFilterLine(triggerReport{Filter: both, Dropped: 2}); !strings.Contains(line, `containing "morning", changed since 2026-03-03`) {
		t.Fatalf("two filters do not stand as one list: %q", line)
	}
}

// A schedule's next tick is shown on the wall clock it is read on, with the
// zone named: the moment travels as UTC, and converting it to whatever the
// caller's host is set to is exactly the confusion the stored zone ends. A row
// that carries no zone, which is every trigger that is not a schedule, keeps
// the local reading it always had.
func TestTriggerListShowsATickOnItsOwnWallClock(t *testing.T) {
	var out strings.Builder
	printTrigger(&out, map[string]any{
		"id": "a1b2c3d4e5f60718", "label": "Schedule", "where": "0 9 * * 1-5", "timezone": "Asia/Tokyo",
		"task": "morning", "state": "standing", "open": true, "fired": float64(0),
		"nextAt": "2026-09-22T00:00:00Z",
	}, triggerReport{})
	if text := out.String(); !strings.Contains(text, "next 2026-09-22 09:00 Asia/Tokyo") {
		t.Fatalf("the tick is not shown on its own wall clock:\n%s", text)
	}
}

// The header says which zone a schedule made now would take, and whether that
// is an answer somebody stored or only the zone this server stands in. The two
// read differently on purpose, and `timezone-get` answers the same pair on its
// own for a caller that only wants to know which clock is in force.
func TestTriggerListSaysWhichZoneIsInForce(t *testing.T) {
	stored := formatTriggers(triggerReport{Now: time.Now(), Zone: "Europe/Berlin", Stored: true})
	if !strings.Contains(stored, "New schedules are read in Europe/Berlin, the stored zone; timezone-set moves it.") {
		t.Fatalf("a stored zone is not said to be one:\n%s", stored)
	}
	bare := formatTriggers(triggerReport{Now: time.Now(), Zone: "UTC"})
	if !strings.Contains(bare, "New schedules are read in UTC, this server's own zone; nobody stored one, timezone-set does.") {
		t.Fatalf("an unstored zone is not said to be the server's:\n%s", bare)
	}
	if strings.Contains(bare, "the stored zone") {
		t.Fatalf("an unstored zone must not read as an answer somebody gave:\n%s", bare)
	}
}

// What a schedule answers beyond its id: the zone that was really applied and
// the tick written out in it, so nobody has to convert a crontab expression by
// hand. Everything that is not a schedule answers nothing here.
func TestAScheduleAnswersItsZoneAndItsNextTick(t *testing.T) {
	line := scheduleLine(map[string]any{"timezone": "Europe/Berlin", "nextAt": "2026-09-22T07:00:00Z"})
	if line != "zone Europe/Berlin, next 2026-09-22 09:00 Europe/Berlin\n" {
		t.Fatalf("the schedule line is %q", line)
	}
	for _, answer := range []map[string]any{
		{},
		{"timezone": "Europe/Berlin"},
		{"nextAt": "2026-09-22T07:00:00Z"},
	} {
		if got := scheduleLine(answer); got != "" {
			t.Fatalf("a trigger that is no schedule answers %q", got)
		}
	}
}

// A bound the trigger has no use for is dropped and said so, never stored in
// silence: a schedule has no batch window, and a caller that named one has to
// read that it was ignored instead of reading the bound back off the row.
func TestABatchWindowOnAScheduleIsSaidToBeIgnored(t *testing.T) {
	if line := ignoredLine(map[string]any{"ignored": "batch"}); line != "--batch is ignored: a schedule has no batch window\n" {
		t.Fatalf("the ignored line is %q", line)
	}
	for _, answer := range []map[string]any{{}, {"ignored": ""}} {
		if got := ignoredLine(answer); got != "" {
			t.Fatalf("a call that named nothing to drop answers %q", got)
		}
	}
}

// `timezone-get` is one line, and it keeps the two zones apart: what somebody
// stored is where the user said they are, what nobody stored is only the zone
// this host happens to run in.
func TestTimezoneGetSaysWhetherAnybodyStoredOne(t *testing.T) {
	stored := timezoneLine(map[string]any{"timezone": "Europe/Berlin", "stored": true, "server": "UTC"})
	if stored != "New schedules are read in Europe/Berlin, the stored zone; this server runs in UTC.\n" {
		t.Fatalf("a stored zone answers %q", stored)
	}
	bare := timezoneLine(map[string]any{"timezone": "UTC", "stored": false, "server": "UTC"})
	if !strings.Contains(bare, "Nobody stored a zone") || !strings.Contains(bare, "timezone-set stores one") {
		t.Fatalf("an unstored zone answers %q", bare)
	}
}
