package cli

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/clirun"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/terminalstate"
	"github.com/marein/dev-cockpit/internal/tmux"
)

func TestStatusOutputCarriesWhatAnAnswerNeeds(t *testing.T) {
	at := time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC)
	report := statusReport{
		Now: at,
		Running: []terminalLine{
			{Kind: "coder", Coder: "claude", ID: "aaa", Name: "Fix the tabs", Project: "cockpit", News: true, Steered: true},
			{Kind: "shell", ID: "bbb", Name: "build", Project: "cockpit"},
		},
		Inactive: []terminalLine{
			{Kind: "coder", Coder: "copilot", ID: "ccc", Name: "Old work", Project: "other", When: at},
		},
		Projects: []projectLine{{Name: "cockpit", Branch: "master"}, {Name: "notes"}},
		Unread:   1,
	}

	out := formatStatus(report, maxInactiveShown)
	for _, want := range []string{
		"Running (2)", "coder claude", "Fix the tabs", "[news]", "id aaa",
		// A steered terminal says so, so the list alone answers whether a
		// coder is the assistant's right now.
		"[steered]",
		"shell", "build",
		"Inactive coders (1)", "Old work", "last used",
		"Projects (2)", "cockpit (master)", "notes",
		"Unread notifications: 1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("status output is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "notes (") {
		t.Fatalf("a project without a repository must not show a branch:\n%s", out)
	}
}

func TestStatusCapsTheResumableTailWithoutHidingIt(t *testing.T) {
	report := statusReport{Now: time.Now()}
	for i := 0; i < maxInactiveShown+3; i += 1 {
		report.Inactive = append(report.Inactive, terminalLine{Kind: "coder", Coder: "claude", ID: "x", Name: "old"})
	}

	out := formatStatus(report, maxInactiveShown)
	if got := strings.Count(out, "last used"); got != 0 {
		t.Fatalf("entries without a timestamp must not print one, got %d", got)
	}
	if !strings.Contains(out, "Inactive coders (13)") {
		t.Fatalf("the full count must stay visible:\n%s", out)
	}
	if !strings.Contains(out, "and 3 older") {
		t.Fatalf("the dropped tail must be named:\n%s", out)
	}
	if strings.Count(out, "  coder claude") != maxInactiveShown {
		t.Fatalf("want %d printed entries:\n%s", maxInactiveShown, out)
	}
}

// A screen that could not be read has to say what is wrong with the terminal,
// not what tmux calls it: whether the session is merely stopped is the whole
// question, and the answer names the way back only where there is one.
func TestScreenErrorSaysWhatIsWrongWithTheTerminal(t *testing.T) {
	capture := errors.New("can't find pane: abc\ntmux capture-pane -p -t abc")

	stopped := screenErrorMessage("abc", terminalstate.Resumable, capture)
	if !strings.Contains(stopped, "`coder-resume abc`") {
		t.Fatalf("a stopped coder session is not offered the resume: %q", stopped)
	}

	// Nothing carries the id, so nothing is offered, and the line says both
	// halves of it: no terminal and no session.
	unknown := screenErrorMessage("abc", terminalstate.Unknown, capture)
	if strings.Contains(unknown, "coder-resume") {
		t.Fatalf("an unknown id offers a resume it cannot keep: %q", unknown)
	}
	if !strings.Contains(unknown, "no session with that id can be resumed") {
		t.Fatalf("an unknown id does not say a session is missing too: %q", unknown)
	}

	// The terminal runs, so the read itself failed, and then what tmux said is
	// the only thing that carries information.
	running := screenErrorMessage("abc", terminalstate.Running, capture)
	if !strings.Contains(running, "is running") || !strings.Contains(running, "can't find pane: abc") {
		t.Fatalf("a running terminal hides why the read failed: %q", running)
	}
	if strings.Contains(running, "tmux capture-pane") {
		t.Fatalf("the tmux command line belongs in no answer: %q", running)
	}
}

// A record that holds no message yet says nothing about a turn. Calling it
// running was what an assistant read for a coder that had only booted, and
// its answer then waited for a turn that had never started.
func TestActivityOutputSaysWhenNothingWasRecorded(t *testing.T) {
	out := formatActivity("t1", map[string]any{"text": "", "finished": false, "empty": true})
	if strings.Contains(out, "still running") || strings.Contains(out, "is over") {
		t.Fatalf("an empty record judges no turn:\n%s", out)
	}
	if !strings.Contains(out, "has not recorded a message yet") {
		t.Fatalf("want the empty record named:\n%s", out)
	}
	out = formatActivity("t1", map[string]any{"text": "user: write the README", "finished": false})
	if !strings.Contains(out, "its turn is still running") || !strings.Contains(out, "user: write the README") {
		t.Fatalf("a prompt just received is a running turn:\n%s", out)
	}
}

// --all lifts the cap: every inactive coder is printed and no tail line claims
// anything was dropped. The default stays the capped list, so the flag is the
// only way to pay for the whole tail.
func TestStatusPrintsEveryInactiveCoderWithoutALimit(t *testing.T) {
	report := statusReport{Now: time.Now()}
	for i := 0; i < maxInactiveShown+3; i += 1 {
		report.Inactive = append(report.Inactive, terminalLine{Kind: "coder", Coder: "claude", ID: "x", Name: "old"})
	}

	out := formatStatus(report, 0)
	if strings.Count(out, "  coder claude") != maxInactiveShown+3 {
		t.Fatalf("want every entry printed:\n%s", out)
	}
	if strings.Contains(out, "older, ask the user") {
		t.Fatalf("a full list must not claim a dropped tail:\n%s", out)
	}
}

// The job-list output is what an answer about a steered coder is built from, so it
// has to carry every field a decision needs, and ownership above all: a
// steering job is a terminal the assistant holds, read here and never guessed
// from what its terminal shows.
func TestJobsOutputCarriesEveryFieldADecisionNeeds(t *testing.T) {
	at := time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC)
	report := jobsReport{
		Now: at,
		Jobs: []jobLine{
			{
				Terminal: "aaa", Name: "readme-task", Project: "cockpit",
				State: "steering", Open: true,
				Wakes: 2, MaxWakes: 10, ExpiresAt: at.Add(2*time.Hour + 51*time.Minute),
				DoneWhen: "README.md exists and names the project",
				Note:     "still writing the file",
			},
			{
				Terminal: "bbb", Name: "tests", Project: "cockpit",
				State: "blocked",
				Wakes: 4, MaxWakes: 10, ExpiresAt: at.Add(-time.Hour),
				DoneWhen: "go test ./... passes",
			},
		},
	}

	out := formatJobs(report)
	for _, want := range []string{
		"Steering (1)", "Closed (1)",
		"steering", "readme-task", "cockpit", "checks 2/10", "2h 51m left", "id aaa",
		"done when: README.md exists and names the project",
		"last check: still writing the file",
		"blocked", "checks 4/10", "closed", "id bbb",
		// The rule itself, at the place it is read.
		"you hold that terminal",
		"belongs to the user or to another assistant",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("job-list output is missing %q:\n%s", want, out)
		}
	}
	// Open first, the order the thread and the page use.
	if strings.Index(out, "id aaa") > strings.Index(out, "id bbb") {
		t.Fatalf("want the open job first:\n%s", out)
	}
	// A closed job has no time left to report, its state says what became of it.
	if strings.Contains(out, "-1h") || strings.Contains(out, "0m left  id bbb") {
		t.Fatalf("a closed job must not print a countdown:\n%s", out)
	}
	// And it stands as that line alone: the criterion and the last report are
	// the longest lines of this output and say nothing a closed job needs
	// said. `job-show` and `--full` bring them back.
	if strings.Contains(out, "go test ./... passes") {
		t.Fatalf("a closed job must not print its criterion:\n%s", out)
	}
}

// A host collects jobs for weeks, and the report a check leaves is a paragraph.
// The closed tail is capped and the note is cut, both visibly: an answer built
// on this output must never read a cut line as the whole text, or a short list
// as everything there is.
func TestJobsOutputCapsTheClosedTailAndTheLongNotes(t *testing.T) {
	report := jobsReport{Now: time.Now()}
	report.Jobs = append(report.Jobs, jobLine{
		Terminal: "open", Name: "running", State: "steering", Open: true, MaxWakes: 10,
		DoneWhen: "the tests pass",
		Note:     strings.Repeat("a very long report ", 40),
	})
	for i := 0; i < maxClosedJobsShown+6; i++ {
		report.Jobs = append(report.Jobs, jobLine{
			Terminal: "x", Name: "old", State: "stopped", MaxWakes: 10,
			DoneWhen: "the tests pass",
			Note:     strings.Repeat("a very long report ", 40),
		})
	}

	out := formatJobs(report)
	if !strings.Contains(out, "Closed (11)") {
		t.Fatalf("the full count has to stay visible:\n%s", out)
	}
	if !strings.Contains(out, "and 6 older") {
		t.Fatalf("the dropped tail has to be named:\n%s", out)
	}
	if got := strings.Count(out, "id x"); got != maxClosedJobsShown {
		t.Fatalf("want %d printed closed jobs, got %d", maxClosedJobsShown, got)
	}
	// The closed ones carry their line and nothing else, so the criterion and
	// the report of the open one are the only ones in this output.
	if got := strings.Count(out, "done when:"); got != 1 {
		t.Fatalf("want the criterion of the open job alone, got %d:\n%s", got, out)
	}
	if got := strings.Count(out, "last check:"); got != 1 {
		t.Fatalf("want the report of the open job alone, got %d:\n%s", got, out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "last check:") && len([]rune(line)) > maxJobNoteRunes+40 {
			t.Fatalf("a note grew past what the cut allows (%d runes):\n%s", len([]rune(line)), line)
		}
	}
	if !strings.Contains(out, "…") {
		t.Fatalf("a cut note has to say that it was cut:\n%s", out)
	}
}

// The criterion is the bulk of every job-list call, so the list cuts it, and the
// cut says how much is missing and which flag brings the rest. Nothing about
// this reaches a check: a check gets its criterion from the store, in its own
// prompt, and never from this output.
func TestJobsOutputCutsTheCriterionAndSaysSo(t *testing.T) {
	doneWhen := longDoneWhen()
	report := jobsReport{
		Now: time.Now(),
		Jobs: []jobLine{{
			Terminal: "aaa", Name: "long-job", State: "steering",
			Open: true, MaxWakes: 10, ExpiresAt: time.Now().Add(time.Hour),
			DoneWhen: doneWhen,
		}},
	}

	out := formatJobs(report)
	if strings.Contains(out, doneWhen) {
		t.Fatalf("a criterion of %d runes has to be cut in the list:\n%s", len([]rune(doneWhen)), out)
	}
	for _, want := range []string{
		"done when: " + string([]rune(doneWhen)[:maxJobNoteRunes]) + "…",
		fmt.Sprintf("[cut: %d of %d runes shown", maxJobNoteRunes, len([]rune(doneWhen))),
		"--full shows the criteria whole",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the cut has to say %q:\n%s", want, out)
		}
	}
}

// --full is the way back to the whole criterion without knowing a terminal id,
// and it composes with the filters, which is the case `job <terminal>` cannot
// serve: the id is what the caller is looking for.
func TestFullPrintsTheCriteriaWhole(t *testing.T) {
	doneWhen := longDoneWhen()
	report := jobsReport{
		Now:    time.Now(),
		Filter: jobsFilter{Contains: "runner", Full: true},
		Jobs: []jobLine{{
			Terminal: "aaa", Name: "long-job", State: "steering",
			Open: true, MaxWakes: 10, ExpiresAt: time.Now().Add(time.Hour),
			DoneWhen: doneWhen,
		}},
	}

	out := formatJobs(report)
	if !strings.Contains(out, "done when: "+doneWhen) {
		t.Fatalf("--full has to print the criterion word for word:\n%s", out)
	}
	if strings.Contains(out, "cut:") {
		t.Fatalf("--full leaves nothing cut:\n%s", out)
	}
}

// longDoneWhen is a criterion past the cut, the shape a real one has: several
// conditions in one sentence.
func longDoneWhen() string {
	return "go build ./... and go test ./... are clean, " +
		strings.Repeat("every runner is green and named in the report with its count, ", 12) +
		"and nothing was committed"
}

// The word is looked for in what a person searches by, and the filter runs
// before the cap, so it reaches jobs the plain list never prints, with the
// terminal ids `job-show` needs.
func TestJobsFilterSearchesTheFieldsAJobIsFoundBy(t *testing.T) {
	job := jobLine{
		Terminal: "aaa", Name: "notification-wording", State: "done",
		Task: "Rebuild the titles", DoneWhen: "the titles are two lines",
		Note: "the coder reported it finished",
	}
	for _, word := range []string{"notif", "NOTIF", "rebuild", "two lines", "reported"} {
		if !(jobsFilter{Contains: word}).keeps(job) {
			t.Fatalf("%q has to match the job", word)
		}
	}
	for _, word := range []string{"aaa", "nothing like this"} {
		if (jobsFilter{Contains: word}).keeps(job) {
			t.Fatalf("%q must not match the job", word)
		}
	}
	if !(jobsFilter{State: "done"}).keeps(job) {
		t.Fatal("the state filter has to keep a job in that state")
	}
	if (jobsFilter{State: "blocked"}).keeps(job) {
		t.Fatal("the state filter has to drop a job in another state")
	}
}

// --since answers what ran while the user was away, so it asks when something
// last happened on a job, not when it was made.
func TestJobsFilterSinceReadsTheLastChange(t *testing.T) {
	now := time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC)
	fresh := jobLine{State: "done", ChangedAt: now.Add(-time.Hour)}
	old := jobLine{State: "done", ChangedAt: now.Add(-48 * time.Hour)}
	filter := jobsFilter{Since: now.Add(-24 * time.Hour)}
	if !filter.keeps(fresh) {
		t.Fatal("a job that changed inside the window has to stay")
	}
	if filter.keeps(old) {
		t.Fatal("a job that changed before the window has to go")
	}
}

// A job-list call has to be able to say when a span or a date is meant, and refuse
// what it cannot read instead of printing an empty list.
func TestParseSinceReadsSpansAndDates(t *testing.T) {
	now := time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC)
	got, err := parseSince("24h", now)
	if err != nil || !got.Equal(now.Add(-24*time.Hour)) {
		t.Fatalf("want a span back from now, got %v (%v)", got, err)
	}
	got, err = parseSince("2026-03-04", now)
	if err != nil || !got.Equal(time.Date(2026, 3, 4, 0, 0, 0, 0, time.Local)) {
		t.Fatalf("want the local start of that day, got %v (%v)", got, err)
	}
	got, err = parseSince("2026-03-04 09:30", now)
	if err != nil || !got.Equal(time.Date(2026, 3, 4, 9, 30, 0, 0, time.Local)) {
		t.Fatalf("want the local moment, got %v (%v)", got, err)
	}
	for _, raw := range []string{"yesterday", "-2h", "04.03.2026"} {
		if _, err := parseSince(raw, now); err == nil {
			t.Fatalf("want %q refused", raw)
		}
	}
}

// A state that does not exist is a typo, and a typo that prints an empty list
// reads as "there is nothing", which is the wrong answer.
func TestTheStateFlagRefusesWhatNoJobIsIn(t *testing.T) {
	if _, err := (jobsFilter{State: "finished"}).parse("", time.Now()); err == nil {
		t.Fatal("want an unknown state refused")
	}
	ready, err := (jobsFilter{State: " DONE "}).parse("", time.Now())
	if err != nil || ready.State != "done" {
		t.Fatalf("want the state normalized, got %q (%v)", ready.State, err)
	}
}

// The closed jobs are counted per state, which answers how a hundred of them
// ended without printing one, and a narrowed list says what it left out so a
// short list is never read as everything there is.
func TestJobsOutputCountsTheClosedStatesAndNamesTheFilter(t *testing.T) {
	report := jobsReport{
		Now:     time.Now(),
		Filter:  jobsFilter{Contains: "notif", State: "done"},
		Dropped: 12,
	}
	for i := 0; i < 3; i++ {
		report.Jobs = append(report.Jobs, jobLine{Terminal: "d", State: "done", DoneWhen: "x"})
	}
	report.Jobs = append(report.Jobs,
		jobLine{Terminal: "b", State: "blocked", DoneWhen: "x"},
		jobLine{Terminal: "e", State: "expired", DoneWhen: "x"},
	)

	out := formatJobs(report)
	for _, want := range []string{
		"done 3, blocked 1, expired 1",
		`containing "notif"`, "in state done", "12 other jobs are not shown",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("job-list output is missing %q:\n%s", want, out)
		}
	}
}

// --all is the emergency exit: the cap is gone and every closed job is printed.
func TestAllListsEveryClosedJob(t *testing.T) {
	report := jobsReport{Now: time.Now(), Filter: jobsFilter{All: true}}
	for i := 0; i < maxClosedJobsShown+6; i++ {
		report.Jobs = append(report.Jobs, jobLine{Terminal: "x", State: "stopped", DoneWhen: "the tests pass"})
	}

	out := formatJobs(report)
	if got := strings.Count(out, "id x"); got != maxClosedJobsShown+6 {
		t.Fatalf("want every closed job printed, got %d", got)
	}
	if strings.Contains(out, "older") {
		t.Fatalf("nothing is left over with --all:\n%s", out)
	}
}

// A closed job's criterion and its last report are left out of the list, and
// `--full` is the way back to them without a terminal id: a `--contains` hit
// that fell in one of the two has to be readable where it was found.
func TestFullGivesAClosedJobItsTwoLinesBack(t *testing.T) {
	report := jobsReport{
		Now:    time.Now(),
		Filter: jobsFilter{Full: true},
		Jobs: []jobLine{{
			Terminal: "aaa", Name: "old", State: "done", MaxWakes: 10,
			DoneWhen: "the tests pass", Note: "everything was green",
		}},
	}

	out := formatJobs(report)
	for _, want := range []string{"done when: the tests pass", "last check: everything was green"} {
		if !strings.Contains(out, want) {
			t.Fatalf("--full has to bring back %q:\n%s", want, out)
		}
	}
}

// The filters run in the command, on the store, so a narrowed call reaches a
// job the capped list would never print, with the terminal id `job-show` takes.
func TestRunJobsFiltersBeforeTheCap(t *testing.T) {
	stateDir := t.TempDir()
	store := testJobStore(t, stateDir)
	now := time.Now().UTC()
	for i := 0; i < maxClosedJobsShown+5; i++ {
		store.Save(assistant.Job{
			Terminal: fmt.Sprintf("term-%d", i), Name: fmt.Sprintf("filler-%d", i),
			State: assistant.JobDone, DoneWhen: "the tests pass",
			CreatedAt: now.Add(time.Duration(i) * time.Minute), UpdatedAt: now.Add(time.Duration(i) * time.Minute),
		})
	}
	// The oldest one, so the plain list caps it away.
	store.Save(assistant.Job{
		Terminal: "wanted", Name: "notification-wording", State: assistant.JobDone,
		DoneWhen: "the titles are two lines", CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	})

	var plain strings.Builder
	if err := runJobs(&plain, inspectOptions{stateDir: stateDir}, jobsFilter{}); err != nil {
		t.Fatalf("job-list: %v", err)
	}
	if strings.Contains(plain.String(), "id wanted") {
		t.Fatalf("this test proves nothing unless the plain list caps that job away:\n%s", plain.String())
	}

	var found strings.Builder
	if err := runJobs(&found, inspectOptions{stateDir: stateDir}, jobsFilter{Contains: "notification"}); err != nil {
		t.Fatalf("job-list --contains: %v", err)
	}
	if !strings.Contains(found.String(), "id wanted") {
		t.Fatalf("the filter has to reach past the cap:\n%s", found.String())
	}

	var since strings.Builder
	if err := runJobs(&since, inspectOptions{stateDir: stateDir}, jobsFilter{Since: now.Add(-30 * time.Minute)}); err != nil {
		t.Fatalf("job-list --since: %v", err)
	}
	if strings.Contains(since.String(), "id wanted") {
		t.Fatalf("a job older than the window must not be listed:\n%s", since.String())
	}
}

// The job-show command is the counterpart to the cut list: one job, nothing cut.
// The note the list would shorten stands whole, and so does the criterion.
func TestJobOutputCarriesTheWholeJob(t *testing.T) {
	at := time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC)
	longNote := strings.TrimSpace(strings.Repeat("a very long report ", 40))
	if len([]rune(longNote)) <= maxJobNoteRunes {
		t.Fatal("this test proves nothing unless the note is longer than the list cut")
	}
	out := formatJob(at, jobLine{
		Terminal: "aaa", Name: "readme-task", Project: "cockpit", Coder: "claude",
		State: "steering", Open: true,
		Wakes: 2, MaxWakes: 10, ExpiresAt: at.Add(2*time.Hour + 51*time.Minute),
		Task:     "Write the README",
		DoneWhen: "README.md exists and names the project",
		Note:     longNote, NoteAt: at,
	})
	for _, want := range []string{
		`Job "readme-task"`, "terminal  aaa", "coder     claude", "project   cockpit",
		"state     steering", "checks    2/10 used", "2h 51m left",
		"task: Write the README",
		"done when: README.md exists and names the project",
		"last check", longNote,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("job-show output is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "…") {
		t.Fatalf("a single job must never be cut:\n%s", out)
	}
}

// The job-show command reads the same store the list reads, and a terminal nobody
// steers is an error that says where to look, not an empty page.
func TestRunJobReadsTheStoreAndRefusesUnknownTerminals(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Now().UTC()
	testJobStore(t, stateDir).Save(assistant.Job{
		Terminal: "aaa", Name: "readme-task", CoderID: "claude",
		DoneWhen: "README.md exists", State: assistant.JobSteering,
		MaxWakes: 10, CreatedAt: now, ExpiresAt: now.Add(4 * time.Hour),
		Note: strings.Repeat("what the check saw ", 30),
	})

	var out strings.Builder
	if err := runJob(&out, inspectOptions{stateDir: stateDir}, "aaa"); err != nil {
		t.Fatalf("job-show: %v", err)
	}
	if !strings.Contains(out.String(), strings.TrimSpace(strings.Repeat("what the check saw ", 30))) {
		t.Fatalf("the whole note has to stand in the output:\n%s", out.String())
	}
	if err := runJob(&out, inspectOptions{stateDir: stateDir}, "bbb"); err == nil || !strings.Contains(err.Error(), "job-list") {
		t.Fatalf("an unknown terminal has to point at the list, got %v", err)
	}
}

// A terminal that was steered, released and steered again by somebody else
// has an entry in two stores, and the open one is the job, whoever's
// directory comes first in the index: job-show reads through the same lookup
// the watcher decides ownership with, so it names the assistant that holds
// the coder now and not the one whose closed entry the index lists first.
func TestRunJobShowsTheOpenJobWhoeverStandsFirstInTheIndex(t *testing.T) {
	stateDir := t.TempDir()
	now := time.Now().UTC()
	const first = "11111111-1111-4111-8111-111111111111"
	const second = "22222222-2222-4222-8222-222222222222"
	store := assistant.NewStore(stateDir)
	// Save prepends, so the one saved last stands first in the index.
	store.Save(assistant.Instance{Summary: assistant.Summary{ID: second, Title: "Side quest", CoderID: "claude"}})
	store.Save(assistant.Instance{Summary: assistant.Summary{ID: first, Title: "Release work", CoderID: "claude"}})
	jobs := assistant.NewJobs(store)
	jobs.Of(first).Save(assistant.Job{
		Terminal: "aaa", Name: "old-task", CoderID: "claude", DoneWhen: "the old criterion",
		State: assistant.JobDone, MaxWakes: 10, CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(3 * time.Hour),
	})
	jobs.Of(second).Save(assistant.Job{
		Terminal: "aaa", Name: "new-task", CoderID: "claude", DoneWhen: "the new criterion",
		State: assistant.JobSteering, MaxWakes: 10, CreatedAt: now, ExpiresAt: now.Add(4 * time.Hour),
	})
	if all := jobs.All(); len(all) != 2 || all[0].Owner != first || all[0].State != assistant.JobDone {
		t.Fatalf("this test proves nothing unless the closed job's owner stands first in the index, got %+v", all)
	}

	var out strings.Builder
	if err := runJob(&out, inspectOptions{stateDir: stateDir}, "aaa"); err != nil {
		t.Fatalf("job-show: %v", err)
	}
	for _, want := range []string{`Job "new-task"`, "steered by Side quest", "state     steering", "done when: the new criterion"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("job-show has to show the open job, missing %q:\n%s", want, out.String())
		}
	}
	for _, stale := range []string{"old-task", "Release work", "state     done"} {
		if strings.Contains(out.String(), stale) {
			t.Fatalf("job-show showed the closed job the index lists first (%q):\n%s", stale, out.String())
		}
	}
}

// A criterion stored as several lines is one line in the list and its own
// lines in the single job view: the list needs one line per job, the lookup
// shows what is stored, and neither cuts a word.
func TestAMultilineDoneWhenFoldsInTheListAndStandsInTheJob(t *testing.T) {
	doneWhen := "the tests pass\nthe report is written\nnothing was committed"
	job := jobLine{
		Terminal: "aaa", Name: "readme-task", State: "steering",
		Open: true, MaxWakes: 10,
		ExpiresAt: time.Now().Add(time.Hour), DoneWhen: doneWhen,
	}

	list := formatJobs(jobsReport{Now: time.Now(), Jobs: []jobLine{job}})
	if !strings.Contains(list, "done when: the tests pass the report is written nothing was committed\n") {
		t.Fatalf("the list has to fold the criterion to one line without cutting:\n%s", list)
	}

	single := formatJob(time.Now(), job)
	if !strings.Contains(single, "done when: "+doneWhen+"\n") {
		t.Fatalf("the single job has to show the criterion's lines as stored:\n%s", single)
	}
}

func TestJobsOutputSaysWhenNothingIsSteered(t *testing.T) {
	out := formatJobs(jobsReport{Now: time.Now()})
	if !strings.Contains(out, "nothing is being steered") {
		t.Fatalf("want an explicit empty state, got %q", out)
	}
}

// The job-list command reads the same file the server writes. This one goes
// through the real store for that reason.
func TestRunJobsReadsTheStore(t *testing.T) {
	stateDir := t.TempDir()
	store := testJobStore(t, stateDir)
	now := time.Now().UTC()
	store.Save(assistant.Job{
		Terminal: "aaa", Name: "readme-task", Project: "cockpit", CoderID: "claude",
		DoneWhen: "README.md exists", State: assistant.JobSteering,
		MaxWakes: 10, CreatedAt: now, ExpiresAt: now.Add(4 * time.Hour),
	})
	store.Save(assistant.Job{
		Terminal: "bbb", Name: "tests", CoderID: "claude",
		DoneWhen: "the tests pass", State: assistant.JobSteering,
		MaxWakes: 10, CreatedAt: now, ExpiresAt: now.Add(4 * time.Hour),
	})

	var out strings.Builder
	if err := runJobs(&out, inspectOptions{stateDir: stateDir}, jobsFilter{}); err != nil {
		t.Fatalf("job-list: %v", err)
	}
	for _, want := range []string{"readme-task", "tests", "Steering (2)"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("job-list output is missing %q:\n%s", want, out.String())
		}
	}
}

// A state directory that holds no jobs still has to produce a readable report.
func TestRunJobsReadsAnEmptyStateDirectory(t *testing.T) {
	var out strings.Builder
	if err := runJobs(&out, inspectOptions{stateDir: t.TempDir()}, jobsFilter{}); err != nil {
		t.Fatalf("job-list on an empty state dir: %v", err)
	}
	if !strings.Contains(out.String(), "nothing is being steered") {
		t.Fatalf("want an explicit empty state:\n%s", out.String())
	}
}

func TestNotificationsOutputIsEmptyWhenNothingIsUnread(t *testing.T) {
	if out := formatNotifications(nil); !strings.Contains(out, "nothing new") {
		t.Fatalf("want an explicit empty state, got %q", out)
	}
}

func TestNotificationsOutputNamesTargetProjectAndLink(t *testing.T) {
	at := time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC)
	out := formatNotifications([]notify.Notification{
		// A coder writes no text of its own, so its second line is the coder
		// alone, and the project stands in its own column.
		{
			TargetName: "Fix the tabs", Title: "Coder has news.", Detail: "Fix the tabs",
			Project: "cockpit", URL: "/coders/aaa", CreatedAt: at,
		},
		// An assistant writes one, and the job it is about opens that line.
		{
			TargetName: "Release work", Title: "Job done.",
			Detail: "wake-event: The README is written.", URL: "/assistants/bbb", CreatedAt: at,
		},
	})
	for _, want := range []string{
		"Unread notifications (2)",
		"cockpit", "Coder has news.  Fix the tabs", "/coders/aaa",
		"Job done.  wake-event: The README is written.", "/assistants/bbb",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("notification-list output is missing %q:\n%s", want, out)
		}
	}
}

// An entry without a title is one nobody could resolve, or one an older build
// wrote, and it keeps the wording those always had.
func TestNotificationsOutputFallsBackToTheGenericWording(t *testing.T) {
	out := formatNotifications([]notify.Notification{
		{TargetName: "Fix the tabs", Project: "cockpit", URL: "/coders/aaa", CreatedAt: time.Date(2026, 3, 4, 9, 30, 0, 0, time.UTC)},
	})
	if !strings.Contains(out, `Something new in "Fix the tabs"`) {
		t.Fatalf("notification-list output is missing the fallback wording:\n%s", out)
	}
}

// The reservation filter is what keeps an assistant's provider session out of
// the coder lists, every assistant's. The formatting tests above run on hand
// built data, so this one goes through the real store: assistants on disk,
// read the way the inspection commands read them.
func TestReservedSessionsCoverEveryAssistant(t *testing.T) {
	stateDir := t.TempDir()
	seed := func(store *assistant.Store, id string) {
		store.Save(assistant.Instance{
			Summary:         assistant.Summary{ID: id, Title: "seed", CoderID: "claude"},
			NativeSessionID: id,
		})
	}
	seed(assistant.NewStore(stateDir), "11111111-1111-4111-8111-111111111111")
	index, instances, _ := assistant.Paths(stateDir)
	seed(assistant.NewStoreAt(index, instances), "33333333-3333-4333-8333-333333333333")

	hidden := reservedSessions(stateDir)
	for _, id := range []string{"11111111-1111-4111-8111-111111111111", "33333333-3333-4333-8333-333333333333"} {
		if !hidden[id] {
			t.Fatalf("want the assistant %s hidden from the coder lists", id)
		}
	}
	if len(hidden) != 2 {
		t.Fatalf("want exactly the two assistants hidden, got %v", hidden)
	}
}

// emptyMachine gives a test a machine of its own. These commands report what
// this host is running, so on a host that runs a cockpit they would otherwise
// report its terminals, its stored sessions and its notifications, and the test
// would depend on who started it and when. Four things make up that machine: the
// state directory and the projects root, which are already arguments, the home
// directory, where a coder keeps its sessions, and the tmux server, which is
// what "running" means.
func emptyMachine(t *testing.T) inspectOptions {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	// A tmux client takes its server from TMUX when it is set, which it is for
	// every process inside a session, and from TMUX_TMPDIR otherwise. Both have
	// to point away from the host's own server, and an empty directory holds
	// none: the client fails to connect, which is exactly a host with no
	// terminals.
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	return inspectOptions{stateDir: t.TempDir(), projectsDir: t.TempDir()}
}

// A state directory that holds nothing, and a host that may not even have tmux,
// still have to produce a readable report instead of an error dump.
func TestRunStatusReadsAnEmptyStateDirectory(t *testing.T) {
	var out strings.Builder
	err := runStatus(&out, emptyMachine(t), maxInactiveShown)
	if err != nil {
		t.Fatalf("status on an empty state dir: %v", err)
	}
	for _, want := range []string{"Running (0)", "Projects (0)", "Unread notifications: 0"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("status output is missing %q:\n%s", want, out.String())
		}
	}
}

// A conversation must never be reported as a coder anybody could resume, which
// is the one thing the status output shares with the web surfaces.
func TestRunStatusHidesConversations(t *testing.T) {
	if len(clirun.MissingTools(tmux.RequiredTools)) > 0 {
		// The status command lists no sessions at all without tmux, the same
		// check it makes itself, so there would be nothing to hide here.
		t.Skip("tmux is not installed, so no session reaches the coder lists")
	}
	opts := emptyMachine(t)
	conversation := "44444444-4444-4444-8444-444444444444"
	coderSession := "55555555-5555-4555-8555-555555555555"
	// Both sessions are stored the way claude stores them, so both would be
	// listed as resumable coders. Only the one a conversation drives may
	// disappear, and the other one proves the fixture reaches the list at all.
	storeClaudeSession(t, conversation, opts.projectsDir)
	storeClaudeSession(t, coderSession, opts.projectsDir)
	assistant.NewStore(opts.stateDir).Save(assistant.Instance{
		Summary:         assistant.Summary{ID: conversation, Title: "a conversation", CoderID: "claude"},
		NativeSessionID: conversation,
	})

	var out strings.Builder
	if err := runStatus(&out, opts, maxInactiveShown); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(out.String(), coderSession) {
		t.Fatalf("a stored coder session is missing, so this test would prove nothing:\n%s", out.String())
	}
	if strings.Contains(out.String(), conversation) {
		t.Fatalf("the chat's provider session is reported as a coder:\n%s", out.String())
	}
}

// storeClaudeSession writes one session into the claude store of the machine a
// test was given: a transcript named after the session id, carrying the working
// directory the scan needs.
func storeClaudeSession(t *testing.T, id, cwd string) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}
	dir := filepath.Join(home, ".claude", "projects", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	line := `{"type":"user","sessionId":"` + id + `","cwd":"` + cwd + `","timestamp":"2026-07-26T10:00:00Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(line), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// The startup sweep deletes provider sessions a killed check left behind. What
// it may not delete is a coder somebody named like one: the name is user text,
// the working directory is not, it has to be an assistant's workspace.
func TestTheSweepNeedsBothTheNameAndTheWorkspace(t *testing.T) {
	workspace := "/var/lib/dc/assistant/instances/11111111-1111-4111-8111-111111111111/workspace"
	own := func(dir string) bool { return dir == workspace }
	stray := coder.Session{SessionID: "a", Name: "cockpit check: New conversation", CWD: workspace}
	if !isStrayCheckSession(stray, own) {
		t.Fatal("a check session in an assistant workspace has to be swept")
	}
	for _, kept := range []coder.Session{
		{SessionID: "b", Name: "cockpit check: New conversation", CWD: "/root/projects/demo"},
		{SessionID: "c", Name: "readme-task", CWD: workspace},
		{SessionID: "d", Name: "cockpit check: mine", CWD: ""},
	} {
		if isStrayCheckSession(kept, own) {
			t.Fatalf("a coder that is not a stray check was swept: %+v", kept)
		}
	}
}

// The assistant-list command goes through the running cockpit like coder-activity,
// so the test is about the request it makes and the page it prints: capped like
// the status list, with the dropped tail counted instead of hidden, and the
// caller's own row marked so a turn knows which line is itself.
func TestAssistantListCapsAndNamesTheDroppedTail(t *testing.T) {
	var gotPath, gotQuery string
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		var entries []string
		for i := 0; i < maxAssistantsShown+2; i++ {
			entries = append(entries, fmt.Sprintf(
				`{"id":"conv-%d","title":"Task %d","coderId":"claude","lastMessageAt":"2026-03-04T09:30:00Z","preview":"what the answer said","openJobs":%d}`, i, i, i))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"assistants":[` + strings.Join(entries, ",") + `]}`))
	})

	var out strings.Builder
	if err := runAssistants(&out, inspectOptions{stateDir: dir, assistantID: "conv-1"}, ""); err != nil {
		t.Fatalf("assistant-list: %v", err)
	}
	if gotPath != assistantsPath || gotQuery != "" {
		t.Fatalf("want the bare assistants route, got %q with query %q", gotPath, gotQuery)
	}
	for _, want := range []string{
		fmt.Sprintf("Assistants (%d)", maxAssistantsShown+2),
		"claude", `"Task 0"`, "id conv-0", "what the answer said", "last message ",
		"steering 1", "* is you", "and 2 older",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("assistant-list output is missing %q:\n%s", want, out.String())
		}
	}
	if got := strings.Count(out.String(), "id conv-"); got != maxAssistantsShown {
		t.Fatalf("want %d printed entries, got %d:\n%s", maxAssistantsShown, got, out.String())
	}
	if !strings.Contains(out.String(), `*claude    "Task 1"`) {
		t.Fatalf("want the caller's own row marked:\n%s", out.String())
	}
}

// --contains travels as a query the server filters by, the command only caps
// and prints. An empty answer still says what was searched.
func TestAssistantListPassesTheContainsWord(t *testing.T) {
	var gotQuery string
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"assistants":[]}`))
	})

	var out strings.Builder
	if err := runAssistants(&out, inspectOptions{stateDir: dir}, "release notes"); err != nil {
		t.Fatalf("assistant-list: %v", err)
	}
	if gotQuery != "contains=release+notes" {
		t.Fatalf("want the escaped word in the query, got %q", gotQuery)
	}
	for _, want := range []string{`containing "release notes" (0)`, "none"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("assistant-list output is missing %q:\n%s", want, out.String())
		}
	}
}

// The assistant-show command reads one transcript through the same query shape
// coder-activity uses: entries and full travel to the server, and what the server
// cut stands in the output as it came, note included. The id may be another
// assistant's, reading across is the point of it.
func TestAssistantShowReadsOneTranscript(t *testing.T) {
	var gotPath, gotQuery string
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abc","title":"Fix the tabs","coderId":"claude","messageCount":8,"dropped":6,` +
			`"messages":[{"role":"user","content":"write the README","createdAt":"2026-03-04T09:30:00Z"},` +
			`{"role":"assistant","content":"The READ… [cut: 8 of 900 runes shown, use --full for the whole message]","createdAt":"2026-03-04T09:31:00Z"}]}`))
	})

	var out strings.Builder
	if err := runAssistantShow(&out, inspectOptions{stateDir: dir}, "abc", "", 0, false); err != nil {
		t.Fatalf("assistant-show: %v", err)
	}
	if gotPath != assistantsPath+"/abc" || gotQuery != "" {
		t.Fatalf("want the bare assistant route, got %q with query %q", gotPath, gotQuery)
	}
	if err := runAssistantShow(&out, inspectOptions{stateDir: dir}, "abc", "", 3, true); err != nil {
		t.Fatalf("assistant-show with entries and full: %v", err)
	}
	if gotQuery != "entries=3&full=1" {
		t.Fatalf("want entries and full in the query, got %q", gotQuery)
	}
	for _, want := range []string{
		`Assistant "Fix the tabs" on coder claude, 8 messages.`,
		"Showing the last 2; 6 older are not shown",
		"[user ", "[assistant ",
		"write the README", "runes shown, use --full",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("assistant-show output is missing %q:\n%s", want, out.String())
		}
	}
}

// --contains travels as a query beside entries and full, and the server
// narrows before it windows, so the command only prints. The head line names
// the matches and the whole thread, or three hits read as the whole history,
// and a headline the filter searched stands over its message, or a hit that
// fell in one would be invisible in what is printed.
func TestAssistantShowPassesTheContainsWord(t *testing.T) {
	var gotQuery string
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abc","title":"Fix the tabs","coderId":"claude","messageCount":40,"dropped":3,` +
			`"messages":[{"role":"user","content":"the release notes are wrong","createdAt":"2026-03-04T09:30:00Z"},` +
			`{"role":"cockpit","content":"the suite is green","headline":"Job done: release-task","createdAt":"2026-03-04T09:31:00Z"}]}`))
	})

	var out strings.Builder
	if err := runAssistantShow(&out, inspectOptions{stateDir: dir}, "abc", "release notes", 2, true); err != nil {
		t.Fatalf("assistant-show with contains: %v", err)
	}
	if gotQuery != "entries=2&full=1&contains=release+notes" {
		t.Fatalf("want the escaped word beside entries and full, got %q", gotQuery)
	}
	for _, want := range []string{
		`Assistant "Fix the tabs" on coder claude, 5 of 40 messages contain "release notes".`,
		"Showing the last 2; 3 older matches are not shown",
		"[cockpit 2026-03-04 09:31] Job done: release-task",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("assistant-show output is missing %q:\n%s", want, out.String())
		}
	}
}

// A filter that matched nothing says so as a filter. The unfiltered line
// claims the assistant never said anything, which under a word is a different
// statement and a wrong one.
func TestAssistantShowSaysWhenNothingCarriesTheWord(t *testing.T) {
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"abc","title":"Fix the tabs","coderId":"claude","messageCount":40,"dropped":0,"messages":[]}`))
	})

	var out strings.Builder
	if err := runAssistantShow(&out, inspectOptions{stateDir: dir}, "abc", "nowhere", 0, false); err != nil {
		t.Fatalf("assistant-show without a match: %v", err)
	}
	for _, want := range []string{
		`0 of 40 messages contain "nowhere".`,
		"No message carries that word.",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("assistant-show output is missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "has no messages yet") {
		t.Fatalf("a filtered reading must not claim an empty thread:\n%s", out.String())
	}
}

// testJobStore is one assistant's job store in a fresh state directory: the
// jobs live inside the assistant that steers them now, so a test that is about
// the listing still needs one to hang them on.
func testJobStore(t *testing.T, stateDir string) *assistant.JobStore {
	t.Helper()
	const owner = "11111111-1111-4111-8111-111111111111"
	store := assistant.NewStore(stateDir)
	store.Save(assistant.Instance{Summary: assistant.Summary{
		ID: owner, Title: "Release work", CoderID: "claude",
	}})
	return assistant.NewJobs(store).Of(owner)
}

// A listing that spans several assistants names who steers each job, and a
// listing of your own does not: in your own list the answer is always you.
func TestJobsOutputNamesTheOwnerOnlyAcrossAssistants(t *testing.T) {
	mine := jobLine{Terminal: "aaa", Name: "readme-task", State: "steering", Open: true, MaxWakes: 10, DoneWhen: "x"}
	theirs := mine
	theirs.OwnerName = "Side quest"

	own := formatJobs(jobsReport{Now: time.Now(), Jobs: []jobLine{mine}})
	if strings.Contains(own, "steered by") {
		t.Fatalf("your own list must not repeat who steers every job:\n%s", own)
	}
	across := formatJobs(jobsReport{Now: time.Now(), Jobs: []jobLine{theirs}, Owners: true})
	if !strings.Contains(across, "steered by: Side quest") {
		t.Fatalf("want the owner named across assistants:\n%s", across)
	}
	if !strings.Contains(across, "only the user can take it off them") {
		t.Fatalf("want the ownership rule spelled out across assistants:\n%s", across)
	}
}

// Whose jobs a call is about: the --assistant flag wins, then the caller's
// own from the group's --as, and a shell without one sees everybody's, which
// is the honest answer to a question nobody can attribute.
func TestJobsOwnerFallsBackToTheCallersOwnID(t *testing.T) {
	if owner, everybody := jobsOwner("", "mine"); owner != "mine" || everybody {
		t.Fatalf("want the caller's own, got %q %v", owner, everybody)
	}
	if owner, everybody := jobsOwner("ALL", "mine"); owner != "" || !everybody {
		t.Fatalf("want every assistant's, got %q %v", owner, everybody)
	}
	if owner, everybody := jobsOwner("theirs", "mine"); owner != "theirs" || everybody {
		t.Fatalf("want the named assistant's, got %q %v", owner, everybody)
	}
	if owner, everybody := jobsOwner("", ""); owner != "" || !everybody {
		t.Fatalf("want everybody's without a turn, got %q %v", owner, everybody)
	}
}
