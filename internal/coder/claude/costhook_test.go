package claude

import (
	"math"
	"os"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/cost"
	costclaude "github.com/marein/dev-cockpit/internal/cost/claude"
	"github.com/marein/dev-cockpit/internal/cost/price"
)

func TestDeleteBooksTheLastCallBeforeTheTranscriptGoes(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	costs := cost.New(t.TempDir(), nil, costclaude.NewSource(root, price.Snapshot, nil))
	alive := 3
	repo := &sessionRepository{stateRoot: root, ollama: newOllamaSessions(t.TempDir()), running: func(id string) bool {
		alive--
		return id == "aaa" && alive > 0
	}}
	c := &Coder{sessions: repo}
	c.BeforeSessionDelete(func(sessionID string) {
		if alive > 0 {
			t.Fatal("the hook ran while claude was still running")
		}
		costs.SessionDeleting(c.ID(), sessionID)
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
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(line("2") + "\n")
	_ = f.Close()

	if err := c.SessionRepository().DeleteSession("aaa"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the transcript is still there: %v", err)
	}
	if err := costs.Settle(); err != nil {
		t.Fatal(err)
	}
	if got := costs.Query(time.UTC, cost.Span{}).Total; math.Abs(got-4) > 1e-9 {
		t.Fatalf("booked %v before the delete, want 4", got)
	}
}

func TestADeleteRefusesWhileClaudeStillRunsTheSession(t *testing.T) {
	root := t.TempDir()
	path := writeTranscript(t, root, "p1", "bbb", cwdLine(t.TempDir()))
	hooked := false
	repo := &sessionRepository{stateRoot: root, ollama: newOllamaSessions(t.TempDir()), running: func(string) bool { return true }}
	repo.beforeDelete = func(string) { hooked = true }
	if err := repo.DeleteSession("bbb"); err == nil {
		t.Fatal("a session claude still runs was deleted")
	}
	if _, err := os.Stat(path); err != nil || hooked {
		t.Fatalf("the transcript went or the hook ran: %v %v", err, hooked)
	}
}
