package plaintext

import "testing"

// Colours, titles, links and charset switches go, what a carriage return
// and an erase in line leave is what shows, and a control character shows
// nothing.
func TestTerminalIsWhatATerminalShows(t *testing.T) {
	for in, want := range map[string]string{
		"\x1b[32;1m$ go test\x1b[0;m\n":                     "$ go test\n",
		"warn: \x1b]0;title\x07disk\n":                      "warn: disk\n",
		"see \x1b]8;;https://x\x1b\\here\x1b]8;;\x1b\\ now": "see here now",
		"\x1b(Bplain\x1bMup\x1b=":                           "plainup",
		"10%\r50%\r100%\n":                                  "100%\n",
		"long line\rshort\n":                                "shortline\n",
		"crlf\r\n":                                          "crlf\n",
		"section_start:1730000002:step_script\r\x1b[0K\x1b[0K\x1b[36;1mExecuting\x1b[0;m\n": "Executing\n",
		"section_end:1730000003:step_script\r\x1b[0K\n":                                     "\n",
		"abc\x1b[1Kde\n":                           "   de\n",
		"abc\x1b[2Kde\n":                           "   de\n",
		"typo\b\bed\n":                             "tyed\n",
		"a\tb\x00\x07\x7f\u0085\u009bc\n":          "a\tbc\n",
		"bad \xff\xfe utf8\n":                      "bad \ufffd utf8\n",
		"open \x1b]0;never closed\nnext line\n":    "open \nnext line\n",
		"aborted \x1b]0;title\x1b[31mred\x1b[0m\n": "aborted red\n",
		"cut short at the end \x1b[3":              "cut short at the end ",
		"lone escape at the end\x1b":               "lone escape at the end",
		"bidi \u202e and zero width \u200b stay\n": "bidi \u202e and zero width \u200b stay\n",
	} {
		if got := Terminal(in); got != want {
			t.Errorf("Terminal(%q) = %q, want %q", in, got, want)
		}
	}
}
