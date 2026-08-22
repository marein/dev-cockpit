package cli

import (
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/assistant"
)

// A coder's signal is not classified: whether it finished, asks something or
// waits for a permission, it says the same sentence, and the coder is named in
// the line below it.
func TestCoderNewsSaysNoMoreThanThatThereIsNews(t *testing.T) {
	title, detail := coderNews("rename-watch-to-steer", "dev-cockpit")
	if title != "Coder has news." {
		t.Fatalf("want the generic coder wording, got %q", title)
	}
	if detail != `"rename-watch-to-steer" - dev-cockpit` {
		t.Fatalf("want the name and the project below it, got %q", detail)
	}
}

// A shell reports when a foreground command ended, so that is what it says.
func TestShellNewsSaysTheCommandFinished(t *testing.T) {
	title, detail := shellNews("git", "dev-cockpit")
	if title != "Command finished." {
		t.Fatalf("want the shell wording, got %q", title)
	}
	if detail != `"git" - dev-cockpit` {
		t.Fatalf("want the name and the project below it, got %q", detail)
	}
}

// A target without a project is named alone, the line never carries a dangling
// separator.
func TestATargetWithoutAProjectStandsAlone(t *testing.T) {
	if _, detail := coderNews("loose-session", ""); detail != `"loose-session"` {
		t.Fatalf("want the name alone, got %q", detail)
	}
}

// A backup belongs to no project, so the archive name is the whole lower line,
// and the title says how the job ended.
func TestBackupNewsNamesTheArchive(t *testing.T) {
	title, detail := backupNews("nightly", true)
	if title != "Backup ready." {
		t.Fatalf("want the finished wording, got %q", title)
	}
	if detail != `"nightly"` {
		t.Fatalf("want the archive name alone, got %q", detail)
	}
	if title, _ := backupNews("nightly", false); title != "Backup failed." {
		t.Fatalf("want the failure wording, got %q", title)
	}
}

// A plain answer says in its title only that the assistant answered, so the
// entry carries the first words of the answer itself.
func TestAnAnsweredNotificationCarriesAnExcerpt(t *testing.T) {
	title, detail := assistantNews(assistant.Message{
		State:   assistant.StateComplete,
		Content: "## Done\n\nThe **tests** pass and the [branch](https://example.test) is pushed.",
	})
	if title != "Assistant answered." {
		t.Fatalf("want the answered wording, got %q", title)
	}
	if detail != "Done The tests pass and the branch is pushed." {
		t.Fatalf("want the words without the markup, got %q", detail)
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
// with, and names the job below, from what the report itself wrote down.
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
		title, detail := assistantNews(assistant.Message{
			State:   assistant.StateComplete,
			Content: "the report",
			Wake: &assistant.WakeNote{
				Terminal: "term-1",
				Name:     "steered-notifications-quiet-and-wording",
				Project:  "dev-cockpit",
				Verdict:  string(c.verdict),
			},
		})
		if title != c.want {
			t.Fatalf("want %q, got %q", c.want, title)
		}
		if detail != `"steered-notifications-quiet-and-wording" - dev-cockpit` {
			t.Fatalf("want the job named below the title, got %q", detail)
		}
	}
}

// A report from before the note carried a name names what it can, instead of a
// job nobody can name any more.
func TestAJobReportWithoutANameCarriesNoLowerLine(t *testing.T) {
	title, detail := assistantNews(assistant.Message{
		State: assistant.StateComplete,
		Wake:  &assistant.WakeNote{Terminal: "term-1", Verdict: string(assistant.VerdictDone)},
	})
	if title != "Job done." {
		t.Fatalf("want the ending of the job, got %q", title)
	}
	if detail != "" {
		t.Fatalf("want no lower line without a name, got %q", detail)
	}
}

// A turn that never finished says so, and what it managed to write is still
// the best line about what it was doing.
func TestAFailedAnswerSaysSo(t *testing.T) {
	title, detail := assistantNews(assistant.Message{State: assistant.StateFailed, Content: "half an answer"})
	if title != "Assistant could not finish." {
		t.Fatalf("want the failure wording, got %q", title)
	}
	if detail != "half an answer" {
		t.Fatalf("want the words that were written, got %q", detail)
	}
}
