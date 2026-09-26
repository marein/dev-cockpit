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
// waits for a permission, it says the same words, and the coder it happened
// in stands in the line below them.
func TestCoderNewsSaysNoMoreThanThatThereIsNews(t *testing.T) {
	title, detail := coderNews("rename-watch-to-steer")
	if title != "Coder has news." {
		t.Fatalf("want the generic coder wording, got %q", title)
	}
	if detail != "rename-watch-to-steer" {
		t.Fatalf("want the coder in the line below, got %q", detail)
	}
}

// A shell reports when a foreground command ended, so that is what it says.
func TestShellNewsSaysTheCommandFinished(t *testing.T) {
	title, detail := shellNews("git")
	if title != "Command finished." {
		t.Fatalf("want the shell wording, got %q", title)
	}
	if detail != "git" {
		t.Fatalf("want the shell in the line below, got %q", detail)
	}
}

// A backup says how the job ended and names the archive below it.
func TestBackupNewsNamesTheArchive(t *testing.T) {
	title, detail := backupNews("nightly", true)
	if title != "Backup ready." {
		t.Fatalf("want the finished wording, got %q", title)
	}
	if detail != "nightly" {
		t.Fatalf("want the archive in the line below, got %q", detail)
	}
	if title, _ := backupNews("nightly", false); title != "Backup failed." {
		t.Fatalf("want the failure wording, got %q", title)
	}
}

// Every kind of notification is built the same way round: the title says what
// happened and nothing else, the line below says what it happened to. None of
// these kinds writes a text of its own, so that line is the identifier alone,
// and it stands there whole however long it is, which is the whole reason it
// left the title.
func TestEveryKindIsBuiltTheSameWayRound(t *testing.T) {
	run := func(action, failure string) func() (string, string) {
		return func() (string, string) {
			return composeNews(docker.RunView{Action: action, Project: "dev-cockpit", Failure: failure})
		}
	}
	cases := []struct {
		what, title, detail string
		news                func() (string, string)
	}{
		{"coder", "Coder has news.", "fix-login", func() (string, string) { return coderNews("fix-login") }},
		{"shell", "Command finished.", "git", func() (string, string) { return shellNews("git") }},
		{"backup", "Backup ready.", "nightly", func() (string, string) { return backupNews("nightly", true) }},
		{"git question", "Git asks a question.", "push", func() (string, string) { return gitPromptNews("push") }},
		{"compose", "Compose finished.", "Compose up", run("Compose up", "")},
		{"approval", "Assistant asks approval.", "Ops: Compose down with volumes in shop", func() (string, string) {
			return approvalNews("Ops", "Compose down with volumes", "shop")
		}},
		{"approval nobody names", "Assistant asks approval.", "Assistant: a compose command", func() (string, string) {
			return approvalNews("", "", "")
		}},
		{"compose failed", "Compose failed.", "Compose up", run("Compose up", "exit 1")},
		{"long coder", "Coder has news.", "notification-titel-reihenfolge", func() (string, string) {
			return coderNews("notification-titel-reihenfolge")
		}},
		{"long shell", "Command finished.", "npm run build --workspace app", func() (string, string) {
			return shellNews("npm run build --workspace app")
		}},
		{"long backup", "Backup ready.", "dev-cockpit-full-2026-09-19-nightly", func() (string, string) {
			return backupNews("dev-cockpit-full-2026-09-19-nightly", true)
		}},
		{"long git question", "Git asks a question.", "fetch --all --prune --tags", func() (string, string) {
			return gitPromptNews("fetch --all --prune --tags")
		}},
		{"long compose", "Compose failed.", "Compose up with a rebuild", run("Compose up with a rebuild", "exit 1")},
	}
	for _, c := range cases {
		title, detail := c.news()
		if title != c.title {
			t.Fatalf("%s: want %q, got %q", c.what, c.title, title)
		}
		if detail != c.detail {
			t.Fatalf("%s: want %q below it, got %q", c.what, c.detail, detail)
		}
		if n := utf8.RuneCountInString(title); n > newsTitleRunes {
			t.Fatalf("%s: a title of %d runes does not fit %d: %q", c.what, n, newsTitleRunes, title)
		}
	}
}

// A plain answer says in its title only that an answer is ready, so the line
// below it opens with the assistant it came from, which is the most precise
// thing there is for an answer somebody asked for, and then carries the first
// words of the answer itself.
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
	if len([]rune(long)) > assistant.PreviewRunes+1 {
		t.Fatalf("want at most %d runes plus the mark, got %q", assistant.PreviewRunes, long)
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
// with, and the line below names the job and then carries the report: the
// title alone leaves a reader with four open jobs no way to tell which one
// ended, so the job opens the line that has the room for it.
func TestAJobReportSaysHowItEndedAndNamesTheJob(t *testing.T) {
	cases := []struct {
		verdict assistant.Verdict
		want    string
	}{
		{assistant.VerdictDone, "Job done."},
		{assistant.VerdictBlocked, "Job blocked."},
		{assistant.VerdictExpired, "Job expired."},
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
		if detail != "readme-task: The README is written and the tests pass." {
			t.Fatalf("want the job and its report below the title, got %q", detail)
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
	if title != "Job done." {
		t.Fatalf("want the verdict as the title, got %q", title)
	}
	if strings.Contains(strings.ToUpper(detail), "DONE") {
		t.Fatalf("want the verdict out of the excerpt, got %q", detail)
	}
}

// A report from before the note carried a name has nothing narrower than the
// assistant it came from, so that is what opens the line: the rule is the
// most precise identifier there is, and this is the least precise it gets.
func TestAJobReportWithoutANameFallsBackToTheAssistant(t *testing.T) {
	title, detail := assistantNews("Release work", assistant.Message{
		Role:    assistant.RoleCockpit,
		State:   assistant.StateComplete,
		Content: "The job is finished.",
		Note:    &assistant.Note{Source: assistant.NoteCheck, Terminal: "term-1", Verdict: string(assistant.VerdictDone)},
	})
	if title != "Job done." {
		t.Fatalf("want the ending of the job, got %q", title)
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
// surface's own: "Assistant" is right for the one that has not been given a
// name of its own.
func TestAnUnnamedAssistantRingsUnderTheSurfaceName(t *testing.T) {
	if got := assistantNewsName(assistant.Summary{Title: assistant.DefaultTitle}); got != assistant.Name {
		t.Fatalf("want the surface name for an unnamed assistant, got %q", got)
	}
	if got := assistantNewsName(assistant.Summary{Title: "Release work"}); got != "Release work" {
		t.Fatalf("want the assistant's own name, got %q", got)
	}
}

// An answer nobody asked for says a trigger fired, and the line below it
// opens with what fired it and carries the reaction's own answer: that answer
// is the whole reason a trigger was worth firing.
func TestATriggeredAnswerSaysATriggerFiredAndCarriesItsAnswer(t *testing.T) {
	title, detail := assistantNews("Bro", assistant.Message{
		Role:    assistant.RoleAssistant,
		State:   assistant.StateComplete,
		Content: "The coder is done, the **tests** pass.",
		Auto:    true,
		Origin: &assistant.Note{
			Source:   assistant.NoteEvent,
			Headline: "Job done: readme",
			Verdict:  "job-done",
			Terminal: "term-1",
			Trigger:  "sub-1",
			Task:     "summarize what is done",
			Count:    1,
		},
	})
	if title != "Trigger fired." {
		t.Fatalf("want the trigger wording, got %q", title)
	}
	if detail != "Job done: readme: The coder is done, the tests pass." {
		t.Fatalf("want what fired it and the reaction's answer below it, got %q", detail)
	}
}

// Several events inside the batch window arrive as one turn, and how many
// they were stays readable: the headline of a trigger nobody named is that
// count, and it opens the line below like every other identifier.
func TestATriggeredAnswerOfSeveralEventsCarriesTheirNumber(t *testing.T) {
	for _, count := range []int{2, 3, 12} {
		title, detail := assistantNews("Release work", assistant.Message{
			Role:    assistant.RoleAssistant,
			State:   assistant.StateComplete,
			Content: "All of them are done.",
			Auto:    true,
			Origin: &assistant.Note{
				Source:   assistant.NoteEvent,
				Headline: fmt.Sprintf("%d events arrived", count),
				Trigger:  "sub-1",
				Task:     "summarize",
				Count:    count,
			},
		})
		if title != "Trigger fired." {
			t.Fatalf("want the trigger wording, got %q", title)
		}
		if want := fmt.Sprintf("%d events arrived: All of them are done.", count); detail != want {
			t.Fatalf("want %q, got %q", want, detail)
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
				Source:   assistant.NoteEvent,
				Headline: "Coder asks: git",
				Trigger:  "sub-1",
				Count:    1,
			},
		})
		if title != "Trigger broke off." {
			t.Fatalf("want the trigger's failure wording for %s, got %q", state, title)
		}
		if detail != "Coder asks: git: I read the screen and" {
			t.Fatalf("want what fired it and what was written below it, got %q", detail)
		}
		// And the count of a batch opens that line the same way.
		_, detail = assistantNews("Release work", assistant.Message{
			Role: assistant.RoleAssistant, State: state, Auto: true,
			Origin: &assistant.Note{Source: assistant.NoteEvent, Headline: "12 events arrived", Count: 12},
		})
		if detail != "12 events arrived" {
			t.Fatalf("want the count alone where the turn wrote nothing, got %q", detail)
		}
	}
}

// A trigger the user named rings under that name, whatever fired it and
// however many events the window folded into the turn: the headline is the
// one line the note is read by, and the name is what it carries where there
// is one. What fired it stays in the thread, where the fold has room for it.
func TestATriggeredAnswerRingsUnderTheTriggersName(t *testing.T) {
	title, detail := assistantNews("Release work", assistant.Message{
		Role:    assistant.RoleAssistant,
		State:   assistant.StateComplete,
		Content: "The tests pass.",
		Auto:    true,
		Origin: &assistant.Note{
			Source:   assistant.NoteEvent,
			Headline: "nightly readme",
			Event:    "Job done: readme-task in dev-cockpit",
			Trigger:  "sub-1",
			Count:    1,
		},
	})
	if title != "Trigger fired." {
		t.Fatalf("want the trigger wording, got %q", title)
	}
	if detail != "nightly readme: The tests pass." {
		t.Fatalf("want the trigger's name in front of its answer, got %q", detail)
	}
	title, detail = assistantNews("Release work", assistant.Message{
		Role: assistant.RoleAssistant, State: assistant.StateInterrupted, Auto: true, Content: "half",
		Origin: &assistant.Note{Source: assistant.NoteEvent, Headline: "nightly readme", Event: "5 events arrived", Count: 5},
	})
	if title != "Trigger broke off." || detail != "nightly readme: half" {
		t.Fatalf("want the name over the count of a batch, got %q and %q", title, detail)
	}
}

// A headline is the cockpit's own line and no Markdown: what a coder is
// called reaches the line below as it is written.
func TestAHeadlineIsNotReadAsMarkdown(t *testing.T) {
	_, detail := assistantNews("Bro", assistant.Message{
		Role:   assistant.RoleAssistant,
		State:  assistant.StateComplete,
		Auto:   true,
		Origin: &assistant.Note{Source: assistant.NoteEvent, Headline: "Coder *ended*", Count: 1},
	})
	if detail != "Coder *ended*" {
		t.Fatalf("want the name as it is written, got %q", detail)
	}
}

// Every title is one of the fixed sentences and nothing else, whatever the
// job, the trigger, the assistant or the answer is called: the same message
// with two different identifiers rings under one and the same title, and the
// line below is where they part. Every one of them fits the narrowest surface
// a title is read in, so nothing of it is ever cut.
func TestATitleIsTheKindAndNothingElse(t *testing.T) {
	long := strings.Repeat("cockpit ", 20)
	check := func(verdict assistant.Verdict, name string) assistant.Message {
		return assistant.Message{Role: assistant.RoleCockpit, State: assistant.StateComplete, Content: long,
			Note: &assistant.Note{Source: assistant.NoteCheck, Terminal: "term-1", Name: name, Project: long, Verdict: string(verdict)}}
	}
	event := func(state assistant.State, headline string) assistant.Message {
		return assistant.Message{Role: assistant.RoleAssistant, State: state, Auto: true, Content: long,
			Origin: &assistant.Note{Source: assistant.NoteEvent, Headline: headline, Count: 1}}
	}
	compose := func(kind, name string) assistant.Message {
		return assistant.Message{Role: assistant.RoleCockpit, State: assistant.StateComplete, Content: long,
			Note: &assistant.Note{Source: assistant.NoteCompose, Run: "run-1", Name: name, Project: long, Verdict: kind}}
	}
	cases := []struct {
		want   string
		first  assistant.Message
		second assistant.Message
	}{
		{"Compose done.", compose(assistant.ComposeKindDone, "Compose up"), compose(assistant.ComposeKindDone, long)},
		{"Compose failed.", compose(assistant.ComposeKindFailed, "Compose up"), compose(assistant.ComposeKindFailed, long)},
		{"Compose declined.", compose(assistant.ComposeKindDeclined, "Compose up"), compose(assistant.ComposeKindDeclined, long)},
		{"Answer ready.", assistant.Message{State: assistant.StateComplete, Content: long}, assistant.Message{State: assistant.StateComplete, Content: "x"}},
		{"Answer broke off.", assistant.Message{State: assistant.StateFailed, Content: long}, assistant.Message{State: assistant.StateInterrupted, Content: "x"}},
		{"Job done.", check(assistant.VerdictDone, "readme-task"), check(assistant.VerdictDone, long)},
		{"Job blocked.", check(assistant.VerdictBlocked, "readme-task"), check(assistant.VerdictBlocked, long)},
		{"Job expired.", check(assistant.VerdictExpired, "readme-task"), check(assistant.VerdictExpired, long)},
		{"Trigger fired.", event(assistant.StateComplete, "nightly"), event(assistant.StateComplete, long)},
		{"Trigger broke off.", event(assistant.StateFailed, "nightly"), event(assistant.StateInterrupted, long)},
	}
	for _, c := range cases {
		for _, who := range []string{"Bro", assistantNewsName(assistant.Summary{Title: long})} {
			first, _ := assistantNews(who, c.first)
			second, _ := assistantNews(who, c.second)
			if first != c.want || second != c.want {
				t.Fatalf("want %q from both, got %q and %q", c.want, first, second)
			}
			if n := utf8.RuneCountInString(first); n > newsTitleRunes {
				t.Fatalf("a title of %d runes does not fit %d: %q", n, newsTitleRunes, first)
			}
		}
	}
}

// The identifier opens the line below and is never cut there: that is what it
// moved down for. Two jobs whose names part late therefore stay two pieces of
// news, where a title cut to what a kind left over would have rung both the
// same.
func TestTheIdentifierOpensTheLineBelowWhole(t *testing.T) {
	done := func(name string) string {
		_, detail := assistantNews("Bro", assistant.Message{Role: assistant.RoleCockpit, State: assistant.StateComplete,
			Content: "The tests pass.",
			Note:    &assistant.Note{Source: assistant.NoteCheck, Terminal: "term-1", Name: name, Verdict: string(assistant.VerdictDone)}})
		return detail
	}
	first, second := done("notification-titel-reihenfolge-fix"), done("notification-titel-budgetplanung")
	if first == second {
		t.Fatalf("both jobs read the same: %q", first)
	}
	if first != "notification-titel-reihenfolge-fix: The tests pass." {
		t.Fatalf("want the job's name whole in front of the report, got %q", first)
	}
	if second != "notification-titel-budgetplanung: The tests pass." {
		t.Fatalf("want the job's name whole in front of the report, got %q", second)
	}
	// A headline is a line of its own and reaches that place whole too.
	_, detail := assistantNews("Bro", assistant.Message{Role: assistant.RoleAssistant, State: assistant.StateComplete,
		Content: "read", Auto: true,
		Origin: &assistant.Note{Source: assistant.NoteEvent, Headline: "Coder ended: fix-login in dev-cockpit", Count: 1}})
	if detail != "Coder ended: fix-login in dev-cockpit: read" {
		t.Fatalf("want the headline whole in front of the answer, got %q", detail)
	}
}

// No lower line runs past the identifier that opens it plus the excerpt's own
// budget: the identifier is a label wherever it is written and the excerpt is
// what is cut, so a phone clamping the body takes the tail of a text and
// never the name that says which piece of news this is.
func TestTheLowerLineIsTheIdentifierPlusTheExcerpt(t *testing.T) {
	long := strings.Repeat("cockpit ", 20)
	idents := map[string]assistant.Message{
		"readme-task": {Role: assistant.RoleCockpit, State: assistant.StateComplete, Content: long,
			Note: &assistant.Note{Source: assistant.NoteCheck, Terminal: "term-1", Name: "readme-task", Verdict: string(assistant.VerdictDone)}},
		"nightly readme": {Role: assistant.RoleAssistant, State: assistant.StateComplete, Content: long, Auto: true,
			Origin: &assistant.Note{Source: assistant.NoteEvent, Headline: "nightly readme", Count: 1}},
		"Release work": {State: assistant.StateComplete, Content: long},
	}
	for ident, m := range idents {
		detail := ""
		if _, detail = assistantNews("Release work", m); !strings.HasPrefix(detail, ident+": ") {
			t.Fatalf("want %q opening the line, got %q", ident, detail)
		}
		max := utf8.RuneCountInString(ident) + 2 + assistant.PreviewRunes + 1
		if n := utf8.RuneCountInString(detail); n > max {
			t.Fatalf("a line of %d runes does not fit %d: %q", n, max, detail)
		}
	}
}

// A name is a conversation title, written from the first message somebody
// typed, so it runs to a paragraph as often as not. It reaches no title at
// all, and of the lines below it reaches only the one an answer somebody
// asked for writes, where there is nothing narrower to name. assistantNewsName
// cuts it to a label first, so it cannot push the excerpt off a phone's
// screen either.
func TestTheNameIsOnlyInTheLineOfAnAnsweredTurn(t *testing.T) {
	raw := "Cockpit Triggers. " + strings.Repeat("we work on the feature together and a coder is stuck. ", 3)
	if utf8.RuneCountInString(raw) <= 100 {
		t.Fatalf("want a name past a hundred runes to measure with, got %d", utf8.RuneCountInString(raw))
	}
	long := assistantNewsName(assistant.Summary{Title: raw})
	if n := utf8.RuneCountInString(long); n > coder.TitleRunes+1 {
		t.Fatalf("want a paragraph cut to a label, got %d runes: %q", n, long)
	}
	named := []assistant.Message{
		{Role: assistant.RoleCockpit, State: assistant.StateComplete, Content: "The README is written.",
			Note: &assistant.Note{Source: assistant.NoteCheck, Terminal: "t1", Name: "readme-task", Verdict: string(assistant.VerdictDone)}},
		{Role: assistant.RoleAssistant, State: assistant.StateComplete, Content: "x", Auto: true,
			Origin: &assistant.Note{Source: assistant.NoteEvent, Headline: "Coder asks: git", Count: 1}},
	}
	answers := []assistant.Message{
		{State: assistant.StateComplete, Content: "x"},
		{State: assistant.StateFailed, Content: "x"},
	}
	for _, who := range []string{"Zq7", raw, long} {
		for _, m := range append(append([]assistant.Message{}, named...), answers...) {
			title, _ := assistantNews(who, m)
			if strings.Contains(title, who) || strings.Contains(title, "Cockpit") {
				t.Fatalf("the name %q reached the title: %q", who, title)
			}
		}
		for _, m := range named {
			if _, detail := assistantNews(who, m); strings.Contains(detail, oneLine(who)) {
				t.Fatalf("the name %q reached a line that names its own thing: %q", who, detail)
			}
		}
		for _, m := range answers {
			if _, detail := assistantNews(who, m); !strings.HasPrefix(detail, oneLine(who)+": ") {
				t.Fatalf("want the name opening an answer's line, got %q", detail)
			}
		}
	}
}

// A compose run an assistant started rings as what it is, under the command
// that ran, with the note's first line behind it, which names the command no
// second time.
func TestAComposeNoteRingsUnderItsCommand(t *testing.T) {
	m := assistant.Message{Role: assistant.RoleCockpit, State: assistant.StateComplete,
		Content: "On shop: went through, exit status 0.",
		Note:    &assistant.Note{Source: assistant.NoteCompose, Run: "run-1", Name: "Compose up", Project: "shop", Verdict: assistant.ComposeKindDone}}
	title, detail := assistantNews("Bro", m)
	if title != "Compose done." || detail != "Compose up: On shop: went through, exit status 0." {
		t.Fatalf("the news reads %q / %q", title, detail)
	}
}
