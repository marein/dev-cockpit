package tmux

import "testing"

// The control mode connection sends the same keys the CLI path sends, so it
// terminates tmux's flag parsing the same way. Without it a key that starts with
// a dash is read as a flag of send-keys.
func TestSendKeyCommandPassesKeyAsOperand(t *testing.T) {
	cases := map[string]string{
		"Enter":  "send-keys -t work:0.0 -- Enter",
		"Up":     "send-keys -t work:0.0 -- Up",
		"-":      "send-keys -t work:0.0 -- -",
		dashText: "send-keys -t work:0.0 -- " + dashText,
	}
	for key, want := range cases {
		if got := sendKeyCommand("work", key); got != want {
			t.Fatalf("sendKeyCommand(%q) = %q, want %q", key, got, want)
		}
	}
}
