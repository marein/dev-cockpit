package claude

import (
	"strings"
	"testing"
)

// The copy sheet hands over what was said, and nothing else: a tool call is the
// coder's bookkeeping and has no business in text somebody copies. The activity
// reading of the same record still names the tools, it answers the question
// what the session last did.
func TestTheCopyReadingLeavesTheToolCallsOut(t *testing.T) {
	recorded, err := readTranscript(strings.NewReader(strings.Join([]string{userLine, toolUseLine, toolResultLn, answerLine}, "\n")), 0, 0)
	if err != nil {
		t.Fatalf("transcript: %v", err)
	}
	if strings.Contains(recorded.Text, "coder ran") {
		t.Fatalf("a tool line stands in the copy reading:\n%s", recorded.Text)
	}
	for _, want := range []string{"user:\nwrite the README", "coder:\nThe README is written."} {
		if !strings.Contains(recorded.Text, want) {
			t.Fatalf("the copy reading misses %q:\n%s", want, recorded.Text)
		}
	}
}
