package assistant

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// instructionsID is the assistant the pinned instructions are rendered for.
const instructionsID = "11111111-1111-4111-8111-111111111111"

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
	return workspace.instructions(instructionsID)
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
		"A job is a standing arrangement and nothing of yours runs while it stands. A check is one bought turn",
		"a signal from the coder buys one at once",
		"five minutes after the last check at the earliest",
		"read it in the same turn as the answer, never from memory",
		"not \"still running\"",
	)
}

// Everything the user asked for belongs into the criterion itself: a check
// judges nothing else, so a wish kept beside it would be reported done while
// it still stands open.
func TestTheInstructionsPutTheUsersWishIntoTheDoneWhen(t *testing.T) {
	pinned(t,
		"it must carry everything the user asked for, because a check judges nothing else",
	)
}

// The criterion examples name the shapes that hold up in a check: a command,
// an end state, an absence, and for UI work what the page must show, because a
// check can read code, call the running instance and drive a browser. The old
// claim that a check cannot click stays gone.
func TestTheInstructionsGiveDoneWhenShapes(t *testing.T) {
	pinned(t,
		"each judged as its own condition",
		"a command that must pass",
		"an end state that must stand",
		"an absence, no old name left behind",
		"for UI work what the page must show",
		"reading code, a grep, a call against the running instance, a page in the browser",
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
		"put the ones that matter into the done-when",
	)
}

// Acting on the cockpit goes through it, never around it, but restarting it is
// not banned any more: on a host where the cockpit itself is the project, the
// restart is part of the job. The state file and tmux rules stay.
func TestTheInstructionsDoNotBanTheRestart(t *testing.T) {
	pinned(t,
		"never write its state files, never drive its tmux sessions yourself",
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
		"Steer every job you hand to a coder",
		"leave one alone only when the user asks for that",
		"every check costs a turn, so keep the criterion tight",
		"pass it as the normal path",
	)
	if text := instructionsText(t); strings.Contains(text, "Only steer what the user asked to be steered") {
		t.Fatalf("the old opt in steer sentence still stands in the instructions")
	}
}

// The lists are capped and cut, and the way past the caps is in the help of
// every command: the instructions say so instead of repeating the flags, and
// the help has to carry them, which the cli package pins.
func TestTheInstructionsPointAtTheHelpForTheFlags(t *testing.T) {
	pinned(t,
		"Lists are capped and long texts are cut. The flags that narrow or widen them are in `./cockpit <command> --help`",
		"use them sparingly, every rune they fetch is paid for in the answer that carries it",
		"The filters are in `--help`",
	)
}

// Searching is the one way past a cap that has to be named rather than left in
// the help: a turn that does not know a thread can be searched reads it whole
// or gives up on it, and both are paid for in the answer that carries them.
// So `--contains` stands on the assistant-show line and in the sentence about
// the caps, with what it does and not with its flags.
func TestTheInstructionsSayAThreadIsSearched(t *testing.T) {
	pinned(t,
		"assistant-show <id> [--contains <word>]",
		"A long thread and a long job list are searched, never paged through",
		"keeps what carries the word and searches past the cap",
		// And the moment the question is "what happened yesterday", which is
		// the other filter that reaches past a cap.
		"`--since` narrows the same way on `job-list` and `trigger-list`",
	)
}

// The command that shows what is unread is called `notification-list`, the
// object first and the verb last like every other one. Pinned because the
// instructions are what the assistant runs, so an old name here is a command
// that does not exist.
func TestTheInstructionsNameTheNotificationsCommand(t *testing.T) {
	pinned(t,
		"notification-list   # the unread notifications",
	)
	for _, gone := range []string{"assistant news", "assistant notifications"} {
		if text := instructionsText(t); strings.Contains(text, gone) {
			t.Fatalf("the old %q command still stands in the instructions", gone)
		}
	}
}

// Every file an assistant creates or uses lands in `assistant-files/` of its
// own workspace. Pinned so the place does not drift or vanish from the
// instructions.
func TestTheInstructionsNameTheAssistantFilesFolder(t *testing.T) {
	pinned(t,
		"Files you create go into `assistant-files/`",
	)
}

// The instructions are one assistant's: they carry its id and its workspace,
// and every command they list runs through the wrapper of that workspace by
// its absolute path, so a turn knows who it is from the file it reads at
// startup and never has to spell a flag. The name it is called is not in
// there: nothing a turn does needs it.
func TestTheInstructionsCarryTheIdentityAndTheWrapper(t *testing.T) {
	dir := t.TempDir()
	_, workspace, err := New(dir, fakeCoders{runner: &fakeRunner{dir: t.TempDir()}}, Cockpit{
		Executable: "/opt/dev-cockpit/dev-cockpit",
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	text := workspace.instructions(instructionsID)
	wrapper := workspace.Wrapper(instructionsID)
	for _, want := range []string{
		"You are `" + instructionsID + "`, and your workspace is",
		"Every cockpit command is `./cockpit …`",
		"run any other way, a command is refused wherever an owner is needed",
		"From another directory it is `" + wrapper + "`, which is what a line like `cd <project> && …` needs",
		"\n./cockpit status   # coders, shells and projects, with what has news\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the instructions are missing %q", want)
		}
	}
	// The short call in every example, the absolute path exactly once.
	if n := strings.Count(text, wrapper); n != 1 {
		t.Fatalf("want the absolute path named once, found it %d times", n)
	}
	commands := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "./cockpit ") {
			commands++
		}
		if strings.Contains(line, "/opt/dev-cockpit/dev-cockpit") || strings.Contains(line, "--as ") || strings.Contains(line, "--state-dir") {
			t.Fatalf("a command spelled without the wrapper: %q", line)
		}
	}
	if commands < 20 {
		t.Fatalf("want every command through the short call, found %d lines", commands)
	}
}

// A criterion is written by the assistant, so its instructions say what a
// check can afford: cheap looks, and the long runs in the coder's task with
// the proof in the criterion.
func TestTheInstructionsKeepLongRunsOutOfTheCriterion(t *testing.T) {
	pinned(t,
		"What a check verifies itself has to be cheap, seconds to a few minutes",
		"Long runs, a suite, an e2e pass, a build, belong into the coder's task",
		"into the criterion goes the proof that the coder ran them and reported the result",
	)
}

// The instructions say what having several assistants means: they share the
// memory, everything of one conversation is separate, reading across is
// allowed and writing is not, and nobody hands work to anybody but a coder.
func TestTheInstructionsExplainSeveralAssistants(t *testing.T) {
	pinned(t,
		"Several assistants run side by side",
		"The other workspaces stand at",
		"You may read another assistant whole, its messages and its files, and you write only your own",
		"There is no way to message another assistant",
		"You cannot delete yourself either",
		// A delete takes the whole assistant, the triggers with it, so the
		// one sentence that says what goes has to name them: an assistant
		// that leaves them out tells the user its schedules survive.
		"removes it for good, thread, jobs and triggers",
		"the user typing into a terminal does not",
	)
}

// What a check does with what it found lives in the check's own prompt, which
// only a check pays for. What stayed in the file every turn, every check and
// every reaction carries is the chat side of it: steering buys a turn that
// gets the coder going again and answers with a verdict, and a steered coder
// is the assistant's to write into only until its job closes. Both hung on
// the section this file no longer carries, so they are pinned here together
// with its absence.
func TestTheInstructionsKeepOnlyTheChatSideOfACheck(t *testing.T) {
	pinned(t,
		"a turn of its own that reads the job, gets the coder going again and answers with a verdict",
		"a coder you steer is yours to write into while its job is open, and the user's again once it closes",
	)
	for _, gone := range []string{
		"### When a check wakes you",
		"Answer with `DONE:`",
		"Only DONE and BLOCKED reach the user",
	} {
		if text := instructionsText(t); strings.Contains(text, gone) {
			t.Fatalf("the check's own prompt stands in the instructions again: %q", gone)
		}
	}
}

// A file in the workspace reaches the user as a relative link: the render
// layer resolves it against that assistant's workspace and the extension
// decides whether it embeds, plays or downloads, while an absolute path
// renders as a dead link. Pinned so the assistant keeps handing files over
// instead of naming paths.
func TestTheInstructionsTellHowToHandOverAFile(t *testing.T) {
	pinned(t,
		"Link them relative to this workspace",
		"`[the patch](assistant-files/x.patch)` is a download",
		"`![shot](assistant-files/shot.png)` shows the image",
		"video and audio play in the answer",
		"The extension decides which, not the link syntax",
		"An absolute path reaches nobody",
	)
}

// The memory lives outside the workspaces now, so the place to write it is an
// absolute path, and the instructions spell it.
func TestTheInstructionsNameTheMemoryDirectory(t *testing.T) {
	dir := t.TempDir()
	_, workspace, err := New(dir, fakeCoders{runner: &fakeRunner{dir: t.TempDir()}}, Cockpit{})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	_, _, memory := Paths(dir)
	if text := workspace.instructions(instructionsID); !strings.Contains(text, "write it into `"+memory+"/<name>.md`") {
		t.Fatalf("the instructions do not name the memory directory:\n%s", text)
	}
}

// Every assistant gets the instruction files of its own workspace, written
// with its own id, and a memory change reaches all of them. The name it was
// given stays out: a turn reads its id from the file and recognises itself by
// it, which is what every path and every command of its own is built on.
func TestSyncWritesEveryAssistantsOwnInstructions(t *testing.T) {
	dir := t.TempDir()
	svc, workspace, err := New(dir, fakeCoders{runner: &fakeRunner{dir: t.TempDir()}}, Cockpit{})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	one, err := svc.Create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	two, err := svc.Create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Rename(two.ID, "Second one"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := workspace.SaveMemory("", "Likes Go", "the user writes Go"); err != nil {
		t.Fatalf("save memory: %v", err)
	}
	for _, id := range []string{one.ID, two.ID} {
		for _, name := range instructionFiles {
			text := mustRead(t, filepath.Join(workspace.Dir(id), name))
			for _, want := range []string{"You are `" + id + "`", "the user writes Go"} {
				if !strings.Contains(text, want) {
					t.Fatalf("%s of %s is missing %q", name, id, want)
				}
			}
			if strings.Contains(text, "Second one") || strings.Contains(text, DefaultTitle) {
				t.Fatalf("%s of %s carries the name it is called: %s", name, id, text)
			}
		}
	}
}

// The instruction files are shared and rebuilt before every turn, so with
// several assistants two rebuilds meet regularly. A reader that lands in the
// middle of one must see a whole file: half a file is what a coder would read
// as its whole instructions.
func TestConcurrentSyncNeverLeavesHalfAFile(t *testing.T) {
	dir := t.TempDir()
	svc, workspace, err := New(dir, fakeCoders{runner: &fakeRunner{}}, Cockpit{StateDir: dir})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	created, err := svc.Create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	own := workspace.Dir(created.ID)
	// Something to rebuild from, and a body long enough that a partial write
	// would be visible rather than landing in one page.
	if _, err := workspace.SaveMemory("", "Long fact", strings.Repeat("the user likes this. ", 200)); err != nil {
		t.Fatalf("save memory: %v", err)
	}
	want := len(mustRead(t, filepath.Join(own, "CLAUDE.md")))

	stop := make(chan struct{})
	var readers sync.WaitGroup
	readers.Add(1)
	short := make(chan int, 64)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			time.Sleep(time.Millisecond)
			for _, name := range instructionFiles {
				data, err := os.ReadFile(filepath.Join(own, name))
				if err != nil {
					// A rename never leaves the name missing, so a failed read
					// is as bad as a short one.
					short <- -1
					continue
				}
				if len(data) != want {
					short <- len(data)
				}
			}
		}
	}()

	var writers sync.WaitGroup
	for i := 0; i < 4; i++ {
		writers.Add(1)
		go func(n int) {
			defer writers.Done()
			for round := 0; round < 8; round++ {
				// Every rebuild has to write, so the "nothing changed" shortcut
				// cannot be what makes this pass: the memory moves under it.
				if _, err := workspace.SaveMemory(fmt.Sprintf("writer-%d", n), fmt.Sprintf("Writer %d", n), fmt.Sprintf("round %d", round)); err != nil {
					t.Errorf("save: %v", err)
					return
				}
			}
		}(i)
	}
	writers.Wait()
	close(stop)
	readers.Wait()
	close(short)

	// Every reading is either the file as it was or the file as it became, and
	// both are whole; only the sizes change, never a cut one.
	for size := range short {
		if size >= 0 && size < 200 {
			t.Fatalf("a reader saw %d bytes, which is not a whole instruction file", size)
		}
		if size < 0 {
			t.Fatal("a reader found no instruction file at all, so a rebuild is not atomic")
		}
	}
	// And what stands at the end is one rebuild's whole output, not two mixed.
	final := mustRead(t, filepath.Join(own, "CLAUDE.md"))
	if !strings.HasPrefix(final, generatedHeader) || strings.Count(final, generatedHeader) != 1 {
		t.Fatalf("the instruction file is not one whole rebuild:\n%.200s", final)
	}
	if other := mustRead(t, filepath.Join(own, "AGENTS.md")); other != final {
		t.Fatal("the two instruction files disagree after concurrent rebuilds")
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
