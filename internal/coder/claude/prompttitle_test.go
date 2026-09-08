package claude

import (
	"testing"

	"github.com/marein/dev-cockpit/internal/coder"
)

// A session nobody named is titled by its first prompt: claude writes no title
// of its own, so without this an answered session keeps a hexadecimal label.
func TestAPromptBecomesTheTitle(t *testing.T) {
	cases := map[string]struct{ message, want string }{
		"plain text": {
			`{"role":"user","content":"Fix the login redirect"}`,
			"Fix the login redirect",
		},
		"text blocks": {
			`{"role":"user","content":[{"type":"text","text":"Fix the login redirect"}]}`,
			"Fix the login redirect",
		},
		"a tool result carries no intent": {
			`{"role":"user","content":[{"type":"tool_result","content":"ok"}]}`,
			"",
		},
		"a slash command reads as the command": {
			`{"role":"user","content":"<command-message>release</command-message>\n<command-name>/release</command-name>"}`,
			"/release",
		},
		"an injected reminder is not the prompt": {
			`{"role":"user","content":"<system-reminder>read this first</system-reminder>\nFix the login redirect"}`,
			"Fix the login redirect",
		},
		"only the first line, whitespace collapsed": {
			`{"role":"user","content":"  Fix   the  redirect \nand the tests too"}`,
			"Fix the redirect",
		},
		"an assistant message is never a title": {
			`{"role":"assistant","content":"Fix the login redirect"}`,
			"",
		},
		"empty": {`{"role":"user","content":""}`, ""},
	}
	for name, c := range cases {
		if got := promptTitle([]byte(c.message)); got != c.want {
			t.Errorf("%s: promptTitle = %q, want %q", name, got, c.want)
		}
	}
}

// A long prompt is cut on a word boundary, so the title stays readable in a
// tab strip, a menu and a list.
func TestALongPromptIsCutOnAWord(t *testing.T) {
	long := `{"role":"user","content":"Please make the coder name optional everywhere it is asked for, the form included"}`
	got := promptTitle([]byte(long))
	if []rune(got)[len([]rune(got))-1] != '…' {
		t.Fatalf("a cut title must say so: %q", got)
	}
	if n := len([]rune(got)); n > coder.TitleRunes+1 {
		t.Fatalf("title is %d runes, want at most %d", n, coder.TitleRunes+1)
	}
	if got[len(got)-len("…")-1] == ' ' {
		t.Fatalf("the cut kept a trailing space: %q", got)
	}
}
