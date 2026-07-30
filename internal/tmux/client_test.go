package tmux

import (
	"slices"
	"testing"
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
