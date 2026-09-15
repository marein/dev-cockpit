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
	return workspace.instructions(instructionsID, "Release work")
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
		"notification-list   # the unread notifications",
		"`notification-list` for what is unread",
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
		"Every file you create or use goes into `assistant-files/` in this workspace, your own folder",
	)
}

// The instructions are one assistant's: they carry its id, its name and its
// workspace, and every command they list runs through the wrapper of that
// workspace by its absolute path, so a turn knows who it is from the file it
// reads at startup and never has to spell a flag.
func TestTheInstructionsCarryTheIdentityAndTheWrapper(t *testing.T) {
	dir := t.TempDir()
	_, workspace, err := New(dir, fakeCoders{runner: &fakeRunner{dir: t.TempDir()}}, Cockpit{
		Executable: "/opt/dev-cockpit/dev-cockpit",
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	text := workspace.instructions(instructionsID, "Release work")
	wrapper := workspace.Wrapper(instructionsID)
	for _, want := range []string{
		"You are the assistant with the id `" + instructionsID + "`, called \"Release work\"",
		"Every cockpit command below runs through `" + wrapper + "`",
		"a command run any other way is refused wherever an owner is needed",
		"\n" + wrapper + " status   # coders, shells and projects, with what has news\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the instructions are missing %q", want)
		}
	}
	commands := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, wrapper+" ") {
			commands++
		}
		if strings.Contains(line, "/opt/dev-cockpit/dev-cockpit") || strings.Contains(line, "--as ") || strings.Contains(line, "--state-dir") {
			t.Fatalf("a command spelled without the wrapper: %q", line)
		}
	}
	if commands < 20 {
		t.Fatalf("want every command through the wrapper, found %d lines", commands)
	}
}

// The instructions say what having several assistants means: they share the
// memory, everything of one conversation is separate, reading across is
// allowed and writing is not, and nobody hands work to anybody but a coder.
func TestTheInstructionsExplainSeveralAssistants(t *testing.T) {
	pinned(t,
		"The user can run several assistants at once",
		"The other assistants' workspaces stand next to yours",
		"You may read another assistant whole, its messages and its files, and you write only your own",
		"There is no way to send another assistant a message or a task",
		"You also cannot delete yourself",
	)
}

// A file in the workspace reaches the user as a relative link: the render
// layer resolves it against that assistant's workspace and the extension
// decides whether it embeds, plays or downloads, while an absolute path
// renders as a dead link. Pinned so the assistant keeps handing files over
// instead of naming paths.
func TestTheInstructionsTellHowToHandOverAFile(t *testing.T) {
	pinned(t,
		"Link such a file with a path relative to this workspace to hand it over",
		"`[the patch](assistant-files/x.patch)` becomes a download",
		"`![shot](assistant-files/shot.png)` shows the picture",
		"a video or an audio file plays in the answer",
		"The extension decides which, the link syntax does not",
		"An absolute path is only a path, it reaches nobody",
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
	if text := workspace.instructions(instructionsID, "x"); !strings.Contains(text, "write it into `"+memory+"/<name>.md`") {
		t.Fatalf("the instructions do not name the memory directory:\n%s", text)
	}
}

// Every assistant gets the instruction files of its own workspace, written
// with its own name, and a memory change reaches all of them.
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
	for _, c := range []struct{ id, title string }{{one.ID, DefaultTitle}, {two.ID, "Second one"}} {
		for _, name := range instructionFiles {
			text := mustRead(t, filepath.Join(workspace.Dir(c.id), name))
			for _, want := range []string{"the id `" + c.id + "`", "called " + fmt.Sprintf("%q", c.title), "the user writes Go"} {
				if !strings.Contains(text, want) {
					t.Fatalf("%s of %s is missing %q", name, c.id, want)
				}
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
