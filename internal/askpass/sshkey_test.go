package askpass

import "testing"

// A terminal's question of the ssh agent stands under a key of its own, is
// news like a proxied question, and takes a typed answer, never a decision.
// The action's end is visible on Done.
func TestAnSSHKeyQuestionStandsUnderItsOwnKey(t *testing.T) {
	b := New(t.TempDir())
	a := b.BeginSSHKey("abc", "Shell: build")
	got := make(chan string, 1)
	go func() {
		text, ok := a.Ask(Question{Kind: KindSSHKey, Action: "Shell: build", Prompt: "Enter the passphrase", Unlock: true})
		if !ok {
			text = "denied"
		}
		got <- text
	}()
	q := *waitQuestion(t, a)
	if id, ok := SSHKeyID(q.Key); !ok || id != "abc" || q.Kind != KindSSHKey || !q.External || !q.Unlock || q.Action != "Shell: build" {
		t.Fatalf("the question reads %+v", q)
	}
	if _, ok := SSHKeyID("shop"); ok {
		t.Fatal("a project name reads as an ssh key question")
	}
	if a.Decide(q.ID, Decision{Approved: true}) {
		t.Fatal("the question took a decision")
	}
	if !a.Answer(q.ID, "secret", false) {
		t.Fatal("the answer was refused")
	}
	if text := <-got; text != "secret" {
		t.Fatalf("the agent read %q", text)
	}
	a.End()
	select {
	case <-a.Done():
	default:
		t.Fatal("Done is open after the end")
	}
}

// On a git action the agent's question stands under the action's key and its
// kind of caller, and a cancel reads as a refusal.
func TestAnSSHKeyQuestionOnAGitAction(t *testing.T) {
	b := New(t.TempDir())
	a := b.Begin("shop", "push")
	defer a.End()
	got := make(chan bool, 1)
	go func() {
		_, ok := a.Ask(Question{Kind: KindSSHKey, Action: "Git push in shop"})
		got <- ok
	}()
	q := *waitQuestion(t, a)
	if q.Key != "shop" || q.Project != "shop" || q.External || q.Kind != KindSSHKey {
		t.Fatalf("the question reads %+v", q)
	}
	a.Answer(q.ID, "", true)
	if <-got {
		t.Fatal("a cancel read as a yes")
	}
	if !a.Cancelled() {
		t.Fatal("the cancel is not remembered")
	}
}
