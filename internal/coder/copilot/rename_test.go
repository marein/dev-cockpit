package copilot

import (
	"testing"

	"github.com/marein/dev-cockpit/internal/coder"
)

// The command goes in as is: no key before or after it, the cockpit cannot
// know the input line or an open dialog, and any key could do harm there.
func TestRenameSendsOnlyTheCommand(t *testing.T) {
	var c coder.Coder = &Coder{}
	if coder.RenameMode(c) != coder.RenameSend {
		t.Fatal("copilot must offer rename")
	}
	input := (&Coder{}).RenameInput("new name")
	if len(input) != 1 || input[0].Prompt != "/rename new name" || input[0].Control != "" {
		t.Fatalf("want only the command, got %+v", input)
	}
}
