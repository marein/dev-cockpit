package cli

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/docker"
)

// A coder's signal is not classified: whether it finished, asks something or
// waits for a permission, it says the same words, and the coder it happened in
// stands behind them.
func TestCoderNewsSaysNoMoreThanThatThereIsNews(t *testing.T) {
	title, detail := coderNews("rename-watch-to-steer")
	if title != "Coder has news, rename-watch-to-steer." {
		t.Fatalf("want the generic coder wording with the coder, got %q", title)
	}
	if detail != "" {
		t.Fatalf("want the line below left to the project, got %q", detail)
	}
}

// A shell reports when a foreground command ended, so that is what it says.
func TestShellNewsSaysTheCommandFinished(t *testing.T) {
	title, detail := shellNews("git")
	if title != "Command finished, git." {
		t.Fatalf("want the shell wording with the shell, got %q", title)
	}
	if detail != "" {
		t.Fatalf("want the line below left to the project, got %q", detail)
	}
}

// A backup belongs to no project, so nothing stands below it at all, and the
// title says how the job ended and which archive it was.
func TestBackupNewsNamesTheArchive(t *testing.T) {
	title, detail := backupNews("nightly", true)
	if title != "Backup ready, nightly." {
		t.Fatalf("want the finished wording with the archive, got %q", title)
	}
	if detail != "" {
		t.Fatalf("want nothing below a backup, got %q", detail)
	}
	if title, _ := backupNews("nightly", false); title != "Backup failed, nightly." {
		t.Fatalf("want the failure wording, got %q", title)
	}
}

// Every kind of notification is built the same way round, what happened and
// then what it happened to, so a reader never has to work out which pattern a
// line follows, and a long identifier is shortened rather than dropped in all
// of them. None of these kinds writes a text of its own, so the line below
// stays empty and the entry's project takes it, which every surface already
// draws in its place.
func TestEveryKindIsBuiltTheSameWayRound(t *testing.T) {
	run := func(action, failure string) func() (string, string) {
		return func() (string, string) {
			return composeNews(docker.RunView{Action: action, Project: "dev-cockpit", Failure: failure})
		}
	}
	cases := []struct {
		what, want string
		news       func() (string, string)
	}{
		{"coder", "Coder has news, fix-login.", func() (string, string) { return coderNews("fix-login") }},
		{"shell", "Command finished, git.", func() (string, string) { return shellNews("git") }},
		{"backup", "Backup ready, nightly.", func() (string, string) { return backupNews("nightly", true) }},
		{"git question", "Git asks a question, push.", func() (string, string) { return gitPromptNews("push") }},
		{"compose", "Compose finished, up -d.", run("up -d", "")},
		{"compose failed", "Compose failed, up -d.", run("up -d", "exit 1")},
		{"long coder", "Coder has news, notification-titel-reih…", func() (string, string) {
			return coderNews("notification-titel-reihenfolge")
		}},
		{"long shell", "Command finished, npm run build…", func() (string, string) {
			return shellNews("npm run build --workspace app")
		}},
		{"long backup", "Backup ready, dev-cockpit-full-2026-09-…", func() (string, string) {
			return backupNews("dev-cockpit-full-2026-09-19-nightly", true)
		}},
		{"long git question", "Git asks a question, fetch --all…", func() (string, string) {
			return gitPromptNews("fetch --all --prune --tags")
		}},
		{"long compose", "Compose failed, up -d --build…", run("up -d --build --remove-orphans", "exit 1")},
	}
	for _, c := range cases {
		title, detail := c.news()
		if title != c.want {
			t.Fatalf("%s: want %q, got %q", c.what, c.want, title)
		}
		if detail != "" {
			t.Fatalf("%s: want the line below left to the project, got %q", c.what, detail)
		}
		if n := utf8.RuneCountInString(title); n > newsTitleRunes {
			t.Fatalf("%s: a title of %d runes does not fit %d: %q", c.what, n, newsTitleRunes, title)
		}
	}
}

// A plain answer says in its title only that an answer is ready, so the line
// below it carries the assistant it came from and then the first words of the
// answer itself.
func TestAnAnsweredNotificationCarriesAnExcerpt(t *testing.T) {
	title, detail := assistantNews("Release work", assistant.Message{
		State:   assistant.StateComplete,
		Content: "## Done\n\nThe **tests** pass and the [branch](https://example.test) is pushed.",
	})
	if title != "Answer ready." {
		t.Fatalf("want the answered wording, got %q", title)
	}
	if detail != "Release work: Done The tests pass and the branch is pushed." {
		t.Fatalf("want the name and then the words without the markup, got %q", detail)
	}
}

// A long answer is cut at a word boundary and says that it goes on, and a
// short one arrives whole.
func TestAnExcerptIsCutAtAWord(t *testing.T) {
	short := answerExcerpt("Short and done.")
	if short != "Short and done." {
		t.Fatalf("want a short answer whole, got %q", short)
	}
	long := answerExcerpt(strings.Repeat("cockpit ", 40))
	if !strings.HasSuffix(long, "…") {
		t.Fatalf("want the cut marked, got %q", long)
	}
	if len([]rune(long)) > answerExcerptRunes+1 {
		t.Fatalf("want at most %d runes plus the mark, got %q", answerExcerptRunes, long)
	}
	if strings.Contains(strings.TrimSuffix(long, "…"), "cockp…") || strings.HasSuffix(strings.TrimSuffix(long, "…"), " ") {
		t.Fatalf("want the cut on a word boundary, got %q", long)
	}
	for _, word := range strings.Fields(strings.TrimSuffix(long, "…")) {
		if word != "cockpit" {
			t.Fatalf("want whole words only, got %q in %q", word, long)
		}
	}
}

// A report says how the job ended, in the words of the state it was closed
// with, and names the job behind it: the line below is the check's report and
// says nothing about which job it was, so "Job done." alone leaves a reader
// with four open jobs no way to tell which one ended.
func TestAJobReportSaysHowItEndedAndNamesTheJob(t *testing.T) {
	cases := []struct {
		verdict assistant.Verdict
		want    string
	}{
		{assistant.VerdictDone, "Job done, readme-task."},
		{assistant.VerdictBlocked, "Job blocked, readme-task."},
		{assistant.VerdictExpired, "Job expired, readme-task."},
	}
	for _, c := range cases {
		title, detail := assistantNews("Release work", assistant.Message{
			Role:    assistant.RoleCockpit,
			State:   assistant.StateComplete,
			Content: "The **README** is written and the tests pass.",
			Note: &assistant.Note{
				Source:   assistant.NoteCheck,
				Terminal: "term-1",
				Name:     "readme-task",
				Project:  "dev-cockpit",
				Verdict:  string(c.verdict),
			},
		})
		if title != c.want {
			t.Fatalf("want %q, got %q", c.want, title)
		}
		if detail != "Release work: The README is written and the tests pass." {
			t.Fatalf("want the assistant and its report below the title, got %q", detail)
		}
	}
}

// The report is stored without the verdict it starts with, parseVerdict cuts
// that word off the check's answer before anything is written down, so the
// excerpt never says twice what the title already said once.
func TestAReportsExcerptDoesNotRepeatTheVerdict(t *testing.T) {
	title, detail := assistantNews("Release work", assistant.Message{
		Role:    assistant.RoleCockpit,
		State:   assistant.StateComplete,
		Content: "The tests pass, the branch is pushed.",
		Note:    &assistant.Note{Source: assistant.NoteCheck, Terminal: "term-1", Name: "readme-task", Verdict: string(assistant.VerdictDone)},
	})
	if !strings.HasPrefix(title, "Job done") {
		t.Fatalf("want the verdict in front of the title, got %q", title)
	}
	if strings.Contains(strings.ToUpper(detail), "DONE") {
		t.Fatalf("want the verdict out of the excerpt, got %q", detail)
	}
}

// A report from before the note carried a name names none, instead of a job
// nobody can name any more. The assistant and what the check wrote still stand
// below it.
func TestAJobReportWithoutANameStillCarriesTheReport(t *testing.T) {
	title, detail := assistantNews("Release work", assistant.Message{
		Role:    assistant.RoleCockpit,
		State:   assistant.StateComplete,
		Content: "The job is finished.",
		Note:    &assistant.Note{Source: assistant.NoteCheck, Terminal: "term-1", Verdict: string(assistant.VerdictDone)},
	})
	if title != "Job done." {
		t.Fatalf("want the ending of the job alone, got %q", title)
	}
	if detail != "Release work: The job is finished." {
		t.Fatalf("want the assistant and its report below the title, got %q", detail)
	}
}

// A turn that never finished says so, and what it managed to write is still
// the best line about what it was doing.
func TestAFailedAnswerSaysSo(t *testing.T) {
	title, detail := assistantNews("Release work", assistant.Message{State: assistant.StateFailed, Content: "half an answer"})
	if title != "Answer broke off." {
		t.Fatalf("want the failure wording, got %q", title)
	}
	if detail != "Release work: half an answer" {
		t.Fatalf("want the assistant and the words that were written, got %q", detail)
	}
}

// An assistant nobody named yet still needs a word in the bell, and it is the
// surface's own: "Assistant answered" is right for the one that has not been
// given a name of its own.
func TestAnUnnamedAssistantRingsUnderTheSurfaceName(t *testing.T) {
	if got := assistantNewsName(assistant.Summary{Title: assistant.DefaultTitle}); got != assistant.Name {
		t.Fatalf("want the surface name for an unnamed assistant, got %q", got)
	}
	if got := assistantNewsName(assistant.Summary{Title: "Release work"}); got != "Release work" {
		t.Fatalf("want the assistant's own name, got %q", got)
	}
}

// An answer nobody asked for says a trigger fired and what fired it, and the
// line below it is the reaction's own answer: that answer is the whole reason
// a trigger was worth firing.
func TestATriggeredAnswerSaysATriggerFiredAndCarriesItsAnswer(t *testing.T) {
	title, detail := assistantNews("Bro", assistant.Message{
		Role:    assistant.RoleAssistant,
		State:   assistant.StateComplete,
		Content: "The coder is done, the **tests** pass.",
		Auto:    true,
		Origin: &assistant.Note{
			Source:       assistant.NoteEvent,
			Headline:     "Job done: readme",
			Verdict:      "job-done",
			Terminal:     "term-1",
			Subscription: "sub-1",
			Task:         "summarize what is done",
			Count:        1,
		},
	})
	if title != "Trigger fired, Job done: readme." {
		t.Fatalf("want the trigger wording with what fired it, got %q", title)
	}
	if detail != "Bro: The coder is done, the tests pass." {
		t.Fatalf("want the assistant and the reaction's answer below it, got %q", detail)
	}
}

// Several events inside the batch window arrive as one turn, and how many
// they were stays readable: a count is short, so the title always carries it.
func TestATriggeredAnswerOfSeveralEventsCarriesTheirNumber(t *testing.T) {
	for _, count := range []int{2, 3, 12} {
		title, detail := assistantNews("Release work", assistant.Message{
			Role:    assistant.RoleAssistant,
			State:   assistant.StateComplete,
			Content: "All of them are done.",
			Auto:    true,
			Origin: &assistant.Note{
				Source:       assistant.NoteEvent,
				Headline:     fmt.Sprintf("%d events arrived", count),
				Subscription: "sub-1",
				Task:         "summarize",
				Count:        count,
			},
		})
		if want := fmt.Sprintf("Trigger fired, %d events.", count); title != want {
			t.Fatalf("want %q, got %q", want, title)
		}
		if detail != "Release work: All of them are done." {
			t.Fatalf("want the assistant and the reaction's answer below it, got %q", detail)
		}
	}
}

// A reaction that broke off stays a reaction: the title says the trigger did,
// never that the assistant could not finish something it was asked, and what
// the turn managed to write still stands below it.
func TestATriggeredAnswerThatBrokeOffStaysATrigger(t *testing.T) {
	for _, state := range []assistant.State{assistant.StateFailed, assistant.StateInterrupted} {
		title, detail := assistantNews("Bro", assistant.Message{
			Role:    assistant.RoleAssistant,
			State:   state,
			Content: "I read the screen and",
			Auto:    true,
			Origin: &assistant.Note{
				Source:       assistant.NoteEvent,
				Headline:     "Coder asks: git",
				Subscription: "sub-1",
				Count:        1,
			},
		})
		if title != "Trigger broke off, Coder asks: git." {
			t.Fatalf("want the trigger's failure wording for %s, got %q", state, title)
		}
		if detail != "Bro: I read the screen and" {
			t.Fatalf("want the assistant and what was written below it, got %q", detail)
		}
		// And the count of a batch stands behind the longer wording too.
		title, _ = assistantNews("Release work", assistant.Message{
			Role: assistant.RoleAssistant, State: state, Auto: true,
			Origin: &assistant.Note{Source: assistant.NoteEvent, Headline: "12 events arrived", Count: 12},
		})
		if title != "Trigger broke off, 12 events." {
			t.Fatalf("want the count beside the failure wording, got %q", title)
		}
	}
}

// A headline is the cockpit's own line and no Markdown: what a coder is
// called reaches the title as it is written.
func TestAHeadlineIsNotReadAsMarkdown(t *testing.T) {
	title, _ := assistantNews("Bro", assistant.Message{
		Role:   assistant.RoleAssistant,
		State:  assistant.StateComplete,
		Auto:   true,
		Origin: &assistant.Note{Source: assistant.NoteEvent, Headline: "Coder *ended*", Count: 1},
	})
	if title != "Trigger fired, Coder *ended*." {
		t.Fatalf("want the name as it is written, got %q", title)
	}
}

// No title runs past the box it is read in, and no lower line past the excerpt
// plus the label in front of it. Everything a notification is made of is user
// text of its own length, the assistant's name up to assistant.MaxTitleRunes, a
// job's name, a headline naming a coder and a project, so every branch is
// measured with the longest of each.
func TestNoTitleRunsPastItsBox(t *testing.T) {
	long := strings.Repeat("cockpit ", 20)
	check := func(verdict assistant.Verdict) assistant.Message {
		return assistant.Message{Role: assistant.RoleCockpit, State: assistant.StateComplete, Content: long,
			Note: &assistant.Note{Source: assistant.NoteCheck, Terminal: "term-1", Name: long, Project: long, Verdict: string(verdict)}}
	}
	event := func(state assistant.State, count int) assistant.Message {
		return assistant.Message{Role: assistant.RoleAssistant, State: state, Auto: true, Content: long,
			Origin: &assistant.Note{Source: assistant.NoteEvent, Headline: long, Count: count}}
	}
	cases := []assistant.Message{
		{State: assistant.StateComplete, Content: long},
		{State: assistant.StateFailed, Content: long},
		check(assistant.VerdictDone), check(assistant.VerdictBlocked), check(assistant.VerdictExpired),
		event(assistant.StateComplete, 1), event(assistant.StateComplete, 7),
		event(assistant.StateFailed, 1), event(assistant.StateInterrupted, 7),
	}
	for _, m := range cases {
		for _, who := range []string{"Bro", assistantNewsName(assistant.Summary{Title: long})} {
			title, detail := assistantNews(who, m)
			if n := utf8.RuneCountInString(title); n > newsTitleRunes {
				t.Fatalf("a title of %d runes does not fit %d: %q", n, newsTitleRunes, title)
			}
			// The name opens the lower line and the excerpt keeps its own
			// budget behind it, so the bound is the label, the ": " and the
			// excerpt.
			max := utf8.RuneCountInString(who) + 2 + answerExcerptRunes
			if n := utf8.RuneCountInString(detail); n > max {
				t.Fatalf("a detail of %d runes does not fit %d: %q", n, max, detail)
			}
			if !strings.HasPrefix(detail, who+": ") {
				t.Fatalf("want the assistant in front of the excerpt, got %q", detail)
			}
		}
	}
}

// The identifier is always carried. One too long for the room behind the kind
// is shortened, never dropped: it is the one part that tells two messages of
// the same kind apart, and no other line of the entry names it.
func TestATitleAlwaysCarriesTheIdentifier(t *testing.T) {
	report := func(name string) string {
		title, _ := assistantNews("Bro", assistant.Message{Role: assistant.RoleCockpit, State: assistant.StateComplete,
			Note: &assistant.Note{Source: assistant.NoteCheck, Terminal: "term-1", Name: name, Verdict: string(assistant.VerdictDone)}})
		return title
	}
	if got := report("wake-event"); got != "Job done, wake-event." {
		t.Fatalf("want a job that fits standing whole, got %q", got)
	}
	// A name of one word has no boundary to cut at, so it is cut at the rune,
	// and the mark is the end of the line: a full stop behind it would read as
	// a fourth dot.
	if got := report("notification-titel-reihenfolge-fix"); got != "Job done, notification-titel-reihenfolg…" {
		t.Fatalf("want a long job shortened rather than dropped, got %q", got)
	}
	// A headline has boundaries, and the cut takes one where it is not so far
	// back that the line loses more than it keeps.
	fired := func(headline string) string {
		title, _ := assistantNews("Bro", assistant.Message{Role: assistant.RoleAssistant, State: assistant.StateComplete,
			Auto: true, Origin: &assistant.Note{Source: assistant.NoteEvent, Headline: headline, Count: 1}})
		return title
	}
	if got := fired("Coder ended: fix-login in dev-cockpit"); got != "Trigger fired, Coder ended: fix-login…" {
		t.Fatalf("want the headline cut at a word, got %q", got)
	}
	for _, got := range []string{report("notification-titel-reihenfolge-fix"), fired("Coder ended: fix-login in dev-cockpit")} {
		if n := utf8.RuneCountInString(got); n > newsTitleRunes {
			t.Fatalf("a shortened title of %d runes does not fit %d: %q", n, newsTitleRunes, got)
		}
	}
}

// Two jobs of one kind whose names part late still ring as two: what the title
// keeps of an identifier has to be the part that tells them apart, or four
// open jobs all report the same line.
func TestTwoJobsThatPartLateGetTwoTitles(t *testing.T) {
	done := func(name string) string {
		title, _ := assistantNews("Bro", assistant.Message{Role: assistant.RoleCockpit, State: assistant.StateComplete,
			Content: "The tests pass.",
			Note:    &assistant.Note{Source: assistant.NoteCheck, Terminal: "term-1", Name: name, Verdict: string(assistant.VerdictDone)}})
		return title
	}
	first, second := done("notification-titel-reihenfolge-fix"), done("notification-titel-budgetplanung")
	if first == second {
		t.Fatalf("both jobs ring under one title: %q", first)
	}
	if first != "Job done, notification-titel-reihenfolg…" || second != "Job done, notification-titel-budgetplan…" {
		t.Fatalf("want each name kept up to where they part, got %q and %q", first, second)
	}
}

// A name is a conversation title, written from the first message somebody
// typed, so it runs to a paragraph as often as not. It is nowhere near the
// title, whatever it is: the name opens the line below and appears nowhere
// else, so no branch has to be read twice to see whether a short one still
// slipped in. assistantNewsName cuts it to a label first, so it cannot push
// the excerpt off a phone's screen either.
func TestTheNameIsNeverInTheTitle(t *testing.T) {
	raw := "Cockpit Subscriptions. " + strings.Repeat("we work on the feature together and a coder is stuck. ", 3)
	if utf8.RuneCountInString(raw) <= 100 {
		t.Fatalf("want a name past a hundred runes to measure with, got %d", utf8.RuneCountInString(raw))
	}
	long := assistantNewsName(assistant.Summary{Title: raw})
	if n := utf8.RuneCountInString(long); n > coder.TitleRunes+1 {
		t.Fatalf("want a paragraph cut to a label, got %d runes: %q", n, long)
	}
	title, detail := assistantNews(long, assistant.Message{
		Role:    assistant.RoleCockpit,
		State:   assistant.StateComplete,
		Content: "The README is written.",
		Note: &assistant.Note{Source: assistant.NoteCheck, Terminal: "term-1", Name: "notification-titel-reihenfolge-fix",
			Verdict: string(assistant.VerdictDone)},
	})
	if title != "Job done, notification-titel-reihenfolg…" {
		t.Fatalf("want the kind and the job whatever the name is, got %q", title)
	}
	if detail != long+": The README is written." {
		t.Fatalf("want the label in front of the report, got %q", detail)
	}
	// Every branch, with a name short enough to have fitted under the old
	// wording and with the raw paragraph: neither reaches the title.
	check := func(v assistant.Verdict) assistant.Message {
		return assistant.Message{Role: assistant.RoleCockpit, State: assistant.StateComplete, Content: "x",
			Note: &assistant.Note{Source: assistant.NoteCheck, Terminal: "t1", Name: "wake-event", Verdict: string(v)}}
	}
	event := func(st assistant.State) assistant.Message {
		return assistant.Message{Role: assistant.RoleAssistant, State: st, Auto: true, Content: "x",
			Origin: &assistant.Note{Source: assistant.NoteEvent, Headline: "Coder asks: git", Count: 1}}
	}
	branches := []assistant.Message{
		check(assistant.VerdictDone), check(assistant.VerdictBlocked), check(assistant.VerdictExpired),
		event(assistant.StateComplete), event(assistant.StateInterrupted),
		{State: assistant.StateComplete, Content: "x"}, {State: assistant.StateFailed, Content: "x"},
	}
	if len(branches) != 7 {
		t.Fatalf("want every branch measured, got %d", len(branches))
	}
	for _, who := range []string{"Zq7", raw, long} {
		for _, m := range branches {
			got, detail := assistantNews(who, m)
			if strings.Contains(got, who) || strings.Contains(got, "Cockpit") {
				t.Fatalf("the name %q reached the title: %q", who, got)
			}
			if n := utf8.RuneCountInString(got); n > newsTitleRunes {
				t.Fatalf("a title of %d runes does not fit %d: %q", n, newsTitleRunes, got)
			}
			// The lower line carries the name as one line, so a paragraph
			// that ends in a space is compared against what it becomes.
			if !strings.HasPrefix(detail, oneLine(who)+": ") {
				t.Fatalf("want the name opening the line below, got %q", detail)
			}
		}
	}
}
