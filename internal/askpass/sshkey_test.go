package askpass

import (
	"slices"
	"strings"
	"testing"
)

// A question of the ssh agent stands under a key of its own, is news like a
// proxied question where nobody in the app started what asks, and takes a
// typed answer, never a decision. The UI's own question stays off the news.
func TestAnSSHKeyQuestionStandsUnderItsOwnKey(t *testing.T) {
	b := New(t.TempDir())
	a := b.BeginSSHKey("abc", "Shell", true)
	got := make(chan string, 1)
	go func() {
		text, ok := a.Ask(Question{Kind: KindSSHKey, Action: "Shell", Prompt: "Enter the passphrase", Unlock: true})
		if !ok {
			text = "denied"
		}
		got <- text
	}()
	q := *waitQuestion(t, a)
	if id, ok := SSHKeyID(q.Key); !ok || id != "abc" || q.Kind != KindSSHKey || !q.External || !q.Unlock || q.Action != "Shell" {
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
	ui := b.BeginSSHKey("def", "Git action", false)
	defer ui.End()
	go ui.Ask(Question{Kind: KindSSHKey, Action: "Git action"})
	if q := *waitQuestion(t, ui); q.External {
		t.Fatalf("the UI's question reads %+v", q)
	}
}

// A process of the cockpit's own asks under a token Serve answers: the helper
// prints what answer returns, a refusal fails it, and after end the token
// opens nothing. No question stands for it in the broker.
func TestServeAnswersTheCockpitsOwnProcesses(t *testing.T) {
	b := bridge(t)
	var prompts []string
	env, end := b.Serve(func(prompt string) ([]byte, bool) {
		prompts = append(prompts, prompt)
		return []byte("yes"), len(prompts) == 1
	})
	token := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "DC_ASKPASS_TOKEN="); ok {
			token = v
		}
	}
	if token == "" || !slices.Contains(env, "DC_ASKPASS_SOCKET="+b.socketPath) {
		t.Fatalf("the environment reads %q", env)
	}
	if answer, err := Ask(b.socketPath, token, "Allow use of key?"); err != nil || answer != "yes" {
		t.Fatalf("the first ask read %q, %v", answer, err)
	}
	if len(b.Questions()) != 0 {
		t.Fatal("a served ask stands as a question")
	}
	if _, err := Ask(b.socketPath, token, "again"); err == nil {
		t.Fatal("a refusal reached the helper as an answer")
	}
	end()
	if _, err := Ask(b.socketPath, token, "after the end"); err == nil {
		t.Fatal("an ended token was answered")
	}
	if len(prompts) != 2 || prompts[0] != "Allow use of key?" {
		t.Fatalf("answer saw %q", prompts)
	}
}
