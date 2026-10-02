package claude

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/cost"
	costclaude "github.com/marein/dev-cockpit/internal/cost/claude"
	"github.com/marein/dev-cockpit/internal/cost/price"
)

// The spend a session writes while its claude ends is booked by the delete
// itself, once, and the transcript is gone afterwards, no stub comes back.
func TestADeleteBooksTheLastCallOnceAndRemovesTheTranscript(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	costs := cost.New(t.TempDir(), nil, costclaude.NewSource(root, price.Snapshot, nil, func() int { return cost.DefaultRetention }))
	repo := &sessionRepository{stateRoot: root, ollama: newOllamaSessions(t.TempDir())}
	c := &Coder{sessions: repo}
	c.BeforeSessionDelete(func(sessionID string) {
		if err := costs.BookSession(c.ID(), sessionID); err != nil {
			t.Error(err)
		}
	})

	at := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	// One call of 100,000 output tokens on opus 5.5 at 20 USD per million.
	line := func(id string) string {
		return `{"type":"assistant","sessionId":"aaa","requestId":"req_` + id + `","timestamp":"` + at + `",` +
			`"message":{"id":"msg_` + id + `","model":"claude-opus-5-5","usage":{"input_tokens":0,"output_tokens":100000}}}`
	}
	path := writeTranscript(t, root, "p1", "aaa", cwdLine(cwd), line("1"))
	if err := costs.Collect(); err != nil {
		t.Fatal(err)
	}
	// claude ending: it writes its last call on the way out, after the
	// delete began, and the delete waits for it.
	bin := filepath.Join(t.TempDir(), "claude")
	script := "#!/bin/sh\nsleep 0.3\nprintf '%s\\n' '" + line("2") + "' >> \"$3\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	ending := exec.Command(bin, "--session-id", "aaa", path)
	if err := ending.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = ending.Wait() }()
	time.Sleep(50 * time.Millisecond)

	if err := c.SessionRepository().DeleteSession("aaa"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("the transcript is still there: %v", err)
		}
		got := 0.0
		for _, p := range costs.Query(time.UTC, cost.Span{}).Projects {
			got += p.USD
		}
		if math.Abs(got-4) > 1e-9 {
			t.Fatalf("booked %v, want 4", got)
		}
		if err := costs.Collect(); err != nil {
			t.Fatal(err)
		}
	}
}
