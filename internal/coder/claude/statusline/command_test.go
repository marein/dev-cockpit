package statusline

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// script writes an executable a command entry can name.
func script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "line.sh")
	writeFile(t, path, "#!/bin/sh\n"+body)
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("make the script executable: %v", err)
	}
	return path
}

func commandEntry(line string) []Entry {
	return Normalize([]Entry{
		{Kind: KindValue, Value: "model", Color: "default"},
		{Kind: KindSeparator, Text: "|"},
		{Kind: KindValue, Value: CommandValue, Text: line, Color: "default"},
	})
}

func TestRenderShowsTheFirstLineACommandPrints(t *testing.T) {
	path := script(t, "printf 'first\\r\\nsecond\\n'\n")
	if out := draw(t, commandEntry(path), `{"model": {"display_name": "Opus 5"}}`); out != "Opus 5 \x1b[2m|\x1b[0m first\n" {
		t.Fatalf("the command renders %q", out)
	}
}

// The command reads what claude handed the line, and runs in the coder's
// folder, the way a status line command of claude's own would.
func TestRenderHandsTheCommandThePayloadAndTheFolder(t *testing.T) {
	work := t.TempDir()
	path := script(t, "read -r body\necho \"$(pwd) $body\"\n")
	body := `{"workspace": {"current_dir": "` + work + `"}}`
	out := draw(t, Normalize([]Entry{{Kind: KindValue, Value: CommandValue, Text: path}}), body)
	if out != work+" "+body+"\n" {
		t.Fatalf("the command saw %q, want its folder and the payload", out)
	}
}

// The line is never handed to a shell: what looks like a variable or a second
// command is an argument like any other.
func TestRenderRunsACommandWithoutAShell(t *testing.T) {
	path := script(t, "echo \"$1|$2\"\n")
	out := draw(t, Normalize([]Entry{{Kind: KindValue, Value: CommandValue, Text: path + ` '$HOME' "a; rm -rf b"`}}), "{}")
	if out != "$HOME|a; rm -rf b\n" {
		t.Fatalf("the arguments arrived as %q", out)
	}
}

// A command that fails, or does not end in time, leaves the line together
// with the separator in front of it.
func TestRenderDropsACommandThatFailsOrHangs(t *testing.T) {
	failing := script(t, "echo partial\nexit 3\n")
	if out := draw(t, commandEntry(failing), `{"model": {"display_name": "Opus 5"}}`); out != "Opus 5\n" {
		t.Fatalf("a failing command renders %q", out)
	}
	if out := draw(t, commandEntry("/no/such/program"), `{"model": {"display_name": "Opus 5"}}`); out != "Opus 5\n" {
		t.Fatalf("a missing program renders %q", out)
	}
	if out := draw(t, commandEntry(`unclosed "quote`), `{"model": {"display_name": "Opus 5"}}`); out != "Opus 5\n" {
		t.Fatalf("a line that does not split renders %q", out)
	}
	// The child holds the pipe open too, which is what the group kill is for.
	hanging := script(t, "sleep 60 &\nsleep 60\n")
	start := time.Now()
	if out := draw(t, commandEntry(hanging), `{"model": {"display_name": "Opus 5"}}`); out != "Opus 5\n" {
		t.Fatalf("a hanging command renders %q", out)
	}
	if took := time.Since(start); took > commandTimeout+commandWaitDelay {
		t.Fatalf("a hanging command held the line for %s", took)
	}
}

// A command that prints colors, a link and a tab shows its visible text in the
// entry's own color: the escape sequences go whole and the tab is a space.
func TestRenderShowsTheVisibleTextOfAColoredCommand(t *testing.T) {
	path := script(t, "printf '\\033[31mred\\033[0m\\t\\033]8;;https://example.com\\007link\\033]8;;\\007\\007\\n'\n")
	out := draw(t, Normalize([]Entry{{Kind: KindValue, Value: CommandValue, Text: path, Color: "cyan"}}), "{}")
	if out != "\x1b[36mred link\x1b[0m\n" {
		t.Fatalf("the output renders %q", out)
	}
}
