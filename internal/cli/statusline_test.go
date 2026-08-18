package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/coder/claude/statusline"
)

// runStatusLine runs the command the way claude does: the status JSON on
// stdin, the line read off stdout.
func runStatusLine(t *testing.T, stateDir, stdin string) (string, string) {
	t.Helper()
	cmd := newRootCommand()
	var out, errOut bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"claude", "status-line", "--state-dir", stateDir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("the command failed: %v", err)
	}
	return out.String(), errOut.String()
}

func TestClaudeStatusLineDrawsTheWrittenLine(t *testing.T) {
	dir := t.TempDir()
	entries := []statusline.Entry{
		{Kind: statusline.KindValue, Value: "model", Color: "default"},
		{Kind: statusline.KindSeparator, Text: "|"},
		{Kind: statusline.KindValue, Value: statusline.FreeTextValue, Text: "eax", Color: "default"},
	}
	if err := statusline.Apply(dir, entries); err != nil {
		t.Fatalf("write the line: %v", err)
	}
	out, errOut := runStatusLine(t, dir, `{"model": {"display_name": "Opus 5"}}`)
	if out != "Opus 5 \x1b[2m|\x1b[0m eax\n" || errOut != "" {
		t.Fatalf("the line is %q with %q on stderr", out, errOut)
	}
}

// A cockpit whose line is off, or a state directory that is gone, draws an
// empty line and says nothing about it: claude shows stdout as the line.
func TestClaudeStatusLineDrawsNothingWithoutALine(t *testing.T) {
	for _, dir := range []string{t.TempDir(), "/no/such/state"} {
		out, errOut := runStatusLine(t, dir, `{"model": {"display_name": "Opus 5"}}`)
		if out != "\n" || errOut != "" {
			t.Fatalf("%s draws %q with %q on stderr, want an empty line", dir, out, errOut)
		}
	}
}
