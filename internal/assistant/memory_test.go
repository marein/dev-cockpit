package assistant

import (
	"strings"
	"testing"
)

// instructionsText renders the generated instructions the way a coder reads
// them, with the command block present, so a pinned sentence cannot slip out
// unnoticed.
func instructionsText(t *testing.T) string {
	t.Helper()
	_, workspace, err := New(t.TempDir(), fakeCoders{runner: &fakeRunner{dir: t.TempDir()}}, Cockpit{
		Executable: "/opt/dev-cockpit/dev-cockpit",
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return workspace.instructions()
}

// pinned asserts that every phrase stands in the generated instructions.
func pinned(t *testing.T, wants ...string) {
	t.Helper()
	text := instructionsText(t)
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Fatalf("the instructions are missing %q", want)
		}
	}
}

// The generated instructions separate a steered job from a running check: the
// job stands on its own, a check is one bought turn, and where a job stands is
// read in the same turn, never remembered. The assistant claimed "the check is
// still running" about jobs that were long done, so this paragraph is pinned
// here against silently disappearing.
func TestTheInstructionsSeparateAJobFromACheck(t *testing.T) {
	pinned(t,
		"A job and a check are two things.",
		"a signal from the coder buys one at once",
		"quiet window of five minutes after a check",
		"`job-list` or `job-show` in the same turn",
		"never from memory",
		"not \"still running\"",
	)
}

// Everything the user asked for belongs into the criterion itself: a check
// judges nothing else, so a wish kept beside it would be reported done while
// it still stands open.
func TestTheInstructionsPutTheUsersWishIntoTheDoneWhen(t *testing.T) {
	pinned(t,
		"and put everything the user asked for into it, a check judges nothing else",
	)
}

// The criterion examples name the shapes that hold up in a check: a command,
// an end state, an absence, and for UI work what the page must show, because a
// check can read code, trigger http calls from the cli and drive a browser.
// They are examples rather than a list, and the old claim that a check cannot
// click stays gone.
func TestTheInstructionsGiveDoneWhenShapes(t *testing.T) {
	pinned(t,
		"a check judges every line as its own condition",
		"examples rather than a list",
		"a command that must pass",
		"an end state that must stand",
		"an absence, no old name left behind",
		"a check can read code, trigger http calls from the cli and drive a browser",
	)
	if text := instructionsText(t); strings.Contains(text, "cannot click") {
		t.Fatalf("the old cannot click claim still stands in the instructions")
	}
}

// A project's own gates belong into the brief and into the criterion: the
// coder runs what exists and what the change warrants, and the ones that
// matter land in the done-when. Pinned so the sentence does not vanish from
// the instructions.
func TestTheInstructionsPutTheProjectGatesIntoTheDoneWhen(t *testing.T) {
	pinned(t,
		"A project brings its own gates, linters, tests, static analysis",
		"have the coder run what exists and what the change warrants",
		"the ones that matter belong into the done-when",
	)
}

// Acting on the cockpit goes through it, never around it, but restarting it is
// not banned any more: on a host where the cockpit itself is the project, the
// restart is part of the job. The state file and tmux rules stay.
func TestTheInstructionsDoNotBanTheRestart(t *testing.T) {
	pinned(t,
		"never write its state files, and never drive its tmux sessions yourself",
	)
	if text := instructionsText(t); strings.Contains(text, "never restart it") {
		t.Fatalf("the restart ban still stands in the instructions")
	}
}

// Steering is the default for work the assistant starts: every job handed to a
// coder gets a criterion, and only an explicit wish of the user leaves one
// left alone. The cost awareness stays, checks are paid in turns, so criteria
// stay tight and a job on foreign work still needs a reason. The old wording
// made steering opt in, so its absence is asserted too.
func TestTheInstructionsSteerStartedJobsByDefault(t *testing.T) {
	pinned(t,
		"Steer every job you hand to a coder, with the criterion that fits it",
		"leave one alone only when the user asks for exactly that",
		"Every check still costs a turn, so the criterion stays tight",
		"pass it as the normal path",
	)
	if text := instructionsText(t); strings.Contains(text, "Only steer what the user asked to be steered") {
		t.Fatalf("the old opt in steer sentence still stands in the instructions")
	}
}

// The jobs list is capped and cut, so the instructions have to carry the way
// past it: without the flags the assistant reads a short list as everything
// there is, and a job whose terminal id it never saw cannot be looked up.
func TestTheInstructionsNameTheJobsFlags(t *testing.T) {
	pinned(t,
		"`--contains <word>` keeps the jobs carrying that word",
		"`--state done|blocked|expired|steering`",
		"`--since 24h`",
		"filter before the cap",
		"`--full` prints the criteria whole",
		"`job-list --contains notif --full`",
		"`--all` lists every closed job",
	)
}

// The command that shows what is unread is called `notification-list`, the
// object first and the verb last like every other one. Pinned because the
// instructions are what the assistant runs, so an old name here is a command
// that does not exist.
func TestTheInstructionsNameTheNotificationsCommand(t *testing.T) {
	pinned(t,
		"assistant notification-list   # the unread notifications",
		"`notification-list` for what is unread",
	)
	for _, gone := range []string{"assistant news", "assistant notifications"} {
		if text := instructionsText(t); strings.Contains(text, gone) {
			t.Fatalf("the old %q command still stands in the instructions", gone)
		}
	}
}

// Every file the assistant creates or uses in its workspace lands in
// `assistant-files/`, one fixed place, so it is easy to find and easy to
// clean up. Pinned so the place does not drift or vanish from the
// instructions.
func TestTheInstructionsNameTheAssistantFilesFolder(t *testing.T) {
	pinned(t,
		"Every file you create or use in this workspace goes into `assistant-files/`",
		"easy to find and easy to clean up",
	)
}

// A file in the workspace reaches the user as a relative link: the render
// layer resolves it against the workspace and the extension decides whether
// it embeds, plays or downloads, while an absolute path renders as a dead
// link. Pinned so the assistant keeps handing files over instead of naming
// paths.
func TestTheInstructionsTellHowToHandOverAFile(t *testing.T) {
	pinned(t,
		"Link such a file with a relative path to hand it over",
		"`[the patch](assistant-files/x.patch)` becomes a download",
		"`![shot](assistant-files/shot.png)` shows the picture",
		"a video or an audio file plays in the answer",
		"The extension decides which, the link syntax does not",
		"An absolute path is only a path, it reaches nobody",
	)
}
