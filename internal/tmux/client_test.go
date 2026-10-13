package tmux

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// dashText is the paste that used to fail with "command set-buffer: unknown
// flag -d", a php option a user pastes into a terminal.
const dashText = "-dxdebug.idekey=PHPSTORM"

func TestSendLiteralArgsPassTextAsOperand(t *testing.T) {
	cases := map[string][]string{
		dashText: {"send-keys", "-t", "work:0.0", "-l", "--", dashText},
		"-":      {"send-keys", "-t", "work:0.0", "-l", "--", "-"},
		"ls -l":  {"send-keys", "-t", "work:0.0", "-l", "--", "ls -l"},
	}
	for text, want := range cases {
		if got := sendLiteralArgs("work", text); !slices.Equal(got, want) {
			t.Fatalf("sendLiteralArgs(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestSetBufferArgsPassTextAsOperand(t *testing.T) {
	cases := map[string][]string{
		dashText:   {"set-buffer", "-b", "buf", "--", dashText},
		"--help":   {"set-buffer", "-b", "buf", "--", "--help"},
		"plain\nl": {"set-buffer", "-b", "buf", "--", "plain\nl"},
	}
	for text, want := range cases {
		if got := setBufferArgs("buf", text); !slices.Equal(got, want) {
			t.Fatalf("setBufferArgs(%q) = %v, want %v", text, got, want)
		}
	}
}

func TestSendKeyArgsPassKeyAsOperand(t *testing.T) {
	want := []string{"send-keys", "-t", "work:0.0", "--", "Enter"}
	if got := sendKeyArgs("work", "Enter"); !slices.Equal(got, want) {
		t.Fatalf("sendKeyArgs = %v, want %v", got, want)
	}
}

func TestRenameArgsPassNewNameAsOperand(t *testing.T) {
	want := []string{"rename-session", "-t", "old", "--", "-new"}
	if got := renameArgs("old", "-new"); !slices.Equal(got, want) {
		t.Fatalf("renameArgs = %v, want %v", got, want)
	}
}

func TestSetBufferArgsKeepTrailingSemicolon(t *testing.T) {
	cases := map[string]string{
		"a;":    `a\;`,
		"a\\;":  "a\\\\;",
		";":     `\;`,
		"a;b":   "a;b",
		"a;\n":  "a;\n",
		"x;;\\": "x;;\\",
	}
	for text, want := range cases {
		got := setBufferArgs("buf", text)
		if got[len(got)-1] != want {
			t.Fatalf("setBufferArgs(%q) operand = %q, want %q", text, got[len(got)-1], want)
		}
	}
}

func TestPasteChunksKeepShortTextWhole(t *testing.T) {
	if got := pasteChunks("", 8); got != nil {
		t.Fatalf("pasteChunks(\"\") = %q, want none", got)
	}
	for _, text := range []string{"short", "exactly8", dashText[:8]} {
		if got := pasteChunks(text, 8); !slices.Equal(got, []string{text}) {
			t.Fatalf("pasteChunks(%q) = %q, want one chunk", text, got)
		}
	}
}

func TestPasteChunksSplitLongTextInOrder(t *testing.T) {
	text := strings.Repeat("0123456789abcdef€\n", 6000)
	chunks := pasteChunks(text, pasteChunkSize)
	if len(chunks) < 2 {
		t.Fatalf("pasteChunks of %d bytes = %d chunks, want several", len(text), len(chunks))
	}
	for i, chunk := range chunks {
		if len(chunk) == 0 || len(chunk) > pasteChunkSize {
			t.Fatalf("chunk %d has %d bytes, want 1 to %d", i, len(chunk), pasteChunkSize)
		}
		if !utf8.ValidString(chunk) {
			t.Fatalf("chunk %d is not valid UTF-8", i)
		}
	}
	if strings.Join(chunks, "") != text {
		t.Fatal("chunks joined differ from the text")
	}
}

func TestPasteChunksNeverSplitARune(t *testing.T) {
	for _, r := range []string{"é", "€", "😀"} {
		for inside := 1; inside < len(r); inside++ {
			head := strings.Repeat("a", 8-inside)
			text := head + r + "b"
			want := []string{head, r + "b"}
			if got := pasteChunks(text, 8); !slices.Equal(got, want) {
				t.Fatalf("pasteChunks(%q) = %q, want %q", text, got, want)
			}
		}
	}
}

func TestPasteChunksPassLaterDashChunkAsOperand(t *testing.T) {
	text := strings.Repeat("a", 8) + dashText
	chunks := pasteChunks(text, 8)
	if len(chunks) < 2 || !strings.HasPrefix(chunks[1], "-") {
		t.Fatalf("pasteChunks(%q) = %q, want a second chunk starting with a dash", text, chunks)
	}
	want := []string{"set-buffer", "-b", "buf", "--", chunks[1]}
	if got := setBufferArgs("buf", chunks[1]); !slices.Equal(got, want) {
		t.Fatalf("setBufferArgs(%q) = %v, want %v", chunks[1], got, want)
	}
}
