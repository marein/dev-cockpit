package askpass

import (
	"testing"
	"time"
)

// An approval is a question of the second kind: parked at once, answered
// with a decision, and standing in the same list a git question stands in,
// under its own key and its own kind.
func TestAnApprovalStandsAndTakesADecision(t *testing.T) {
	b := New(t.TempDir())
	moved := 0
	b.OnChange = func() { moved++ }
	a := b.BeginApproval(ApprovalKey("abc"), Question{
		Project: "shop", Action: "Compose down with volumes", Command: "docker compose down -v",
		Dir: "/srv/shop", Assistant: "Ops", Stack: "ops", URL: "/projects/shop/docker/runs/abc",
	})
	if a == nil || !a.Approval() {
		t.Fatal("the approval action was not opened")
	}
	if b.Find(ApprovalKey("abc")) != a || b.Find("shop") != nil {
		t.Fatal("the approval is keyed by its project instead of its run")
	}
	if _, ok := ApprovalRun("shop"); ok {
		t.Fatal("the approval is keyed by its project instead of its run")
	}
	got := make(chan Decision, 1)
	go func() {
		d, ok := a.AskApproval()
		if !ok {
			d = Decision{}
		}
		got <- d
	}()
	var q Question
	deadline := time.Now().Add(5 * time.Second)
	for {
		if list := b.Questions(); len(list) == 1 {
			q = list[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the approval question never stood")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if q.Kind != KindApproval || q.Key != ApprovalKey("abc") || !q.External || q.Assistant != "Ops" || q.Stack != "ops" || q.URL == "" || q.Command == "" {
		t.Fatalf("the question reads %+v", q)
	}
	if run, ok := ApprovalRun(q.Key); !ok || run != "abc" {
		t.Fatal("the key the approval stands under does not name its run")
	}
	if a.Answer(q.ID, "opensesame", false) {
		t.Fatal("an approval took a typed answer")
	}
	if !a.Decide(q.ID, Decision{Approved: true, Remember: true}) {
		t.Fatal("the decision was refused")
	}
	select {
	case d := <-got:
		if !d.Approved || !d.Remember {
			t.Fatalf("the decision arrived as %+v", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the decision never reached the asker")
	}
	if len(b.Questions()) != 0 {
		t.Fatal("the decided question still stands")
	}
	if moved < 2 {
		t.Fatalf("the change hook fired %d times, want the park and the decision", moved)
	}
}

// A denial reads as no, and so does the action ending before anybody decided,
// which is what the timeout does: the asker reads both as not approved and
// the question leaves the list.
func TestADeniedOrEndedApprovalReadsAsNo(t *testing.T) {
	b := New(t.TempDir())
	for _, way := range []string{"deny", "end"} {
		a := b.BeginApproval(ApprovalKey(way), Question{Project: "shop", Action: "down"})
		type outcome struct {
			d  Decision
			ok bool
		}
		got := make(chan outcome, 1)
		go func() {
			d, ok := a.AskApproval()
			got <- outcome{d, ok}
		}()
		var q Question
		deadline := time.Now().Add(5 * time.Second)
		for {
			if list := b.Questions(); len(list) == 1 {
				q = list[0]
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the question never stood")
			}
			time.Sleep(5 * time.Millisecond)
		}
		if way == "deny" {
			a.Decide(q.ID, Decision{Approved: false})
		} else {
			a.End()
		}
		select {
		case o := <-got:
			if o.d.Approved || o.ok != (way == "deny") {
				t.Fatalf("%s: the asker read %+v, %v", way, o.d, o.ok)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: the asker never returned", way)
		}
		if way == "deny" && !a.Cancelled() {
			t.Fatal("a denial is not read as cancelled")
		}
		a.End()
		if len(b.Questions()) != 0 || b.Find(ApprovalKey(way)) != nil {
			t.Fatalf("%s: the question or the action still stands", way)
		}
	}
}

// A git question keeps its shape: keyed by its project, no kind, and it
// still takes a typed answer and no decision.
func TestAGitQuestionIsUnchanged(t *testing.T) {
	b := New(t.TempDir())
	a := b.Begin("shop", "push")
	if a.Approval() {
		t.Fatal("a git action reads as an approval")
	}
	if _, ok := a.AskApproval(); ok {
		t.Fatal("a git action answered an approval")
	}
	got := make(chan string, 1)
	go func() {
		text, _ := a.ask("Enter passphrase:")
		got <- text
	}()
	var q Question
	deadline := time.Now().Add(5 * time.Second)
	for {
		if list := b.Questions(); len(list) == 1 {
			q = list[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the question never stood")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if q.Kind != "" || q.Key != "shop" || q.Project != "shop" || q.Prompt != "Enter passphrase:" {
		t.Fatalf("the git question reads %+v", q)
	}
	if a.Decide(q.ID, Decision{Approved: true}) {
		t.Fatal("a git question took a decision")
	}
	if !a.Answer(q.ID, "secret", false) {
		t.Fatal("the answer was refused")
	}
	if text := <-got; text != "secret" {
		t.Fatalf("the helper read %q", text)
	}
	a.End()
}
