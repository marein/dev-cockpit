package codex

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/clirun"
	"github.com/marein/dev-cockpit/internal/coder"
)

// argv runs a command line through a real shell with a codex that prints
// its arguments, so what is compared is what codex would be handed.
func argv(t *testing.T, command string) []string {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf 'codex'\nfor a in \"$@\"; do printf '\\0%s' \"$a\"; done\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", command)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the command line does not run: %v\n%s", err, command)
	}
	return strings.Split(string(out), "\x00")
}

func sameArgv(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv = %q\nwant   %q", got, want)
	}
}

// The task is the one argument behind the end of options, whatever it holds:
// a leading dash, quotes, a line break or shell code stay text for the shell
// and for codex alike.
func TestTheTaskIsTheOneArgumentBehindTheEndOfOptions(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my 'app' $(touch pwned)")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	task := "-x \"double\" 'single' $(touch pwned) `touch pwned`; touch pwned\nsecond line"
	args := argv(t, runtime{}.StartCommand(coder.SessionStart{Workdir: dir, Task: "  " + task + "\n", AutomaticApproval: true, Model: "-m odd"}))
	sameArgv(t, args, []string{"codex", "--no-daemon", "-c", trustOverride(dir), "-c", paletteTheme, "-c", bellOn, "-c", bellMethod, "-c", bellAlways, "--dangerously-bypass-approvals-and-sandbox", "--model=-m odd", "--", task})
	for _, place := range []string{dir, filepath.Dir(dir), "."} {
		if _, err := os.Stat(filepath.Join(place, "pwned")); err == nil {
			t.Fatalf("shell code of the task or the directory ran in %s", place)
		}
	}
}

func TestAStartWithoutATaskOrApprovalCarriesTheTrustTheThemeAndTheBellAlone(t *testing.T) {
	dir := t.TempDir()
	command := runtime{}.StartCommand(coder.SessionStart{Workdir: dir, Name: "ignored, codex has no name flag"})
	sameArgv(t, argv(t, command), []string{"codex", "--no-daemon", "-c", trustOverride(dir), "-c", `tui.theme="ansi"`, "-c", "tui.notifications=true", "-c", `tui.notification_method="bel"`, "-c", `tui.notification_condition="always"`})
	if !strings.HasPrefix(command, "cd "+clirun.ShellQuote(dir)+" && exec "+sixteenColors+" ") {
		t.Fatalf("the session does not start in its project: %s", command)
	}
}

// A codex at 16 colors draws with the palette alone, so the cockpit's switch
// of the scheme reaches it while it runs, whatever the login profile exports.
func TestCodexRunsAtTheSixteenColorsOfThePalette(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\nprintf '%s %s' \"$TERM\" \"${COLORTERM-unset}\"\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for label, command := range map[string]string{
		"start":  runtime{}.StartCommand(coder.SessionStart{Workdir: dir}),
		"resume": runtime{}.ResumeCommand("01a20f67-5816-76a3-974b-ba6aea57b9bf", dir, false),
	} {
		cmd := exec.Command("sh", "-c", "export TERM=xterm-256color COLORTERM=truecolor; "+command)
		cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: the command line does not run: %v", label, err)
		}
		if got := string(out); got != "screen unset" {
			t.Fatalf("%s: codex sees TERM and COLORTERM %q, want \"screen unset\"", label, got)
		}
	}
}

func TestAResumeNamesTheSessionBehindTheEndOfOptions(t *testing.T) {
	dir := t.TempDir()
	args := argv(t, runtime{}.ResumeCommand("01a20f67-5816-76a3-974b-ba6aea57b9bf", dir, true))
	sameArgv(t, args, []string{"codex", "resume", "--no-daemon", "-c", trustOverride(dir), "-c", paletteTheme, "-c", bellOn, "-c", bellMethod, "-c", bellAlways, "--dangerously-bypass-approvals-and-sandbox", "--", "01a20f67-5816-76a3-974b-ba6aea57b9bf"})
}

// The trust is a TOML value on the command line, so a directory name must
// not be able to close the string or the table it stands in.
func TestTheTrustOverrideQuotesTheDirectory(t *testing.T) {
	got := trustOverride("/work/a\"b\\c}=\x01é")
	want := `projects={"/work/a\"b\\c}=\u0001é"={trust_level="trusted"}}`
	if got != want {
		t.Fatalf("override = %s\nwant       %s", got, want)
	}
}

func TestTheTrustOverrideNamesTheResolvedDirectoryToo(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(root, "real")
	link := filepath.Join(root, "link")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	got := trustOverride(link)
	want := `projects={` + tomlString(link) + `={trust_level="trusted"},` + tomlString(real) + `={trust_level="trusted"}}`
	if got != want {
		t.Fatalf("override = %s\nwant       %s", got, want)
	}
}

func TestCodexMintsItsOwnIDAndTakesNoName(t *testing.T) {
	var rt coder.SessionRuntime = runtime{}
	if rt.UsesProvidedSessionID() {
		t.Fatal("codex takes no session id")
	}
	if naming, ok := rt.(coder.SessionNaming); !ok || naming.NamesSessions() {
		t.Fatal("codex has no name flag")
	}
	if _, ok := rt.(coder.WorkdirTruster); ok {
		t.Fatal("the trust rides the command line, nothing is written into codex's config")
	}
}

func TestCodexFindsADarkBackgroundInEveryTheme(t *testing.T) {
	light := "bg=#ffffff,fg=#000000"
	if got := coder.PaneStyleFor(New(nil), light); got != "bg=#000000,fg=#ffffff" {
		t.Fatalf("under a light terminal the pane is styled %q, codex would draw its diff line numbers in black", got)
	}
	if got := coder.PaneStyleFor(New(nil), ""); got != "bg=#000000,fg=#ffffff" {
		t.Fatalf("before a theme is known the pane is styled %q", got)
	}
}

// codex rings the terminal for a turn's end and for a question, on every one
// and with the plain bell, set on the command line of a start and a resume
// alike, so nothing of it stands in the user's config.
func TestCodexIsToldToRingTheBellOnItsCommandLine(t *testing.T) {
	dir := t.TempDir()
	for name, command := range map[string]string{
		"start":  runtime{}.StartCommand(coder.SessionStart{Workdir: dir}),
		"resume": runtime{}.ResumeCommand("01a20f67-5816-76a3-974b-ba6aea57b9bf", dir, false),
	} {
		args := strings.Join(argv(t, command), "\x00") + "\x00"
		for _, want := range []string{"tui.notifications=true", `tui.notification_method="bel"`, `tui.notification_condition="always"`} {
			if !strings.Contains(args, "\x00-c\x00"+want+"\x00") {
				t.Fatalf("%s: the command line misses -c %s: %q", name, want, args)
			}
		}
		if strings.Contains(args, "hook") {
			t.Fatalf("%s: the command line carries a hook: %q", name, args)
		}
	}
}
