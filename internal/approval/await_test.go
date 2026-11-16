package approval

import (
	"context"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/askpass"
)

// awaiting asks for a tool the way a waiting caller does, its box storing the
// tool and not turning the kind off, and hands the answer over on a channel.
func (f *fixture) awaiting(ctx context.Context, owner, key string, wait time.Duration, remembered *int) <-chan string {
	answer := make(chan string, 1)
	req := Request{Owner: owner, Kind: "tool", Key: key, What: "Use the tool Search", Details: details,
		RememberLabel: "Always allow this tool", Remember: func() { *remembered++ }}
	go func() {
		got, err := f.s.Await(ctx, req, wait)
		if err != nil {
			got = "error: " + err.Error()
		}
		answer <- got
	}()
	return answer
}

func received(t *testing.T, answer <-chan string) string {
	t.Helper()
	select {
	case got := <-answer:
		return got
	case <-time.After(5 * time.Second):
		t.Fatal("the awaited approval never answered")
		return ""
	}
}

// An awaited approval stands like any other, with the words of its box and
// how long it waits, and answers the decision to the caller: approved with the
// box ticked remembers through the request and leaves the kind's switch alone,
// declined remembers nothing. Neither is reported through Ended.
func TestAnAwaitedApprovalAnswersTheDecision(t *testing.T) {
	f := newFixture(t)
	remembered := 0
	answer := f.awaiting(context.Background(), "bot", "toolu_1", 30*time.Minute, &remembered)
	q := f.question(t)
	if q.Kind != askpass.KindApproval || q.Action != "Use the tool Search" || q.Remember != "Always allow this tool" || q.WaitMinutes != 30 || q.Assistant != "Ops" || q.AssistantID != "0a1b2c3d" {
		t.Fatalf("the question reads %+v", q)
	}
	if list := f.s.load(); len(list) != 1 {
		t.Fatalf("the register holds %+v", list)
	}
	f.decide(t, askpass.Decision{Approved: true, Remember: true})
	if got := received(t, answer); got != Approved || remembered != 1 || f.off["tool"] {
		t.Fatalf("the approval answered %q, remembered %d, switches %v", got, remembered, f.off)
	}
	answer = f.awaiting(context.Background(), "bot", "toolu_2", 5*time.Minute, &remembered)
	if q := f.question(t); q.WaitMinutes != 5 {
		t.Fatalf("the question waits %d minutes", q.WaitMinutes)
	}
	f.decide(t, askpass.Decision{Approved: false, Remember: true})
	if got := received(t, answer); got != Declined || remembered != 1 {
		t.Fatalf("the denial answered %q, remembered %d", got, remembered)
	}
	if len(f.endings) != 0 || len(f.s.load()) != 0 || len(f.broker.Questions()) != 0 {
		t.Fatalf("an awaited approval was reported or outlived its answer: %+v", f.endings)
	}
}

// Nobody answering within the wait ends it Expired, the caller giving up and
// the owner going away end it Withdrawn, and the question goes each time.
func TestAnAwaitedApprovalEndsWithoutAnAnswer(t *testing.T) {
	f := newFixture(t)
	remembered := 0
	answer := f.awaiting(context.Background(), "bot", "toolu_1", 50*time.Millisecond, &remembered)
	if got := received(t, answer); got != Expired {
		t.Fatalf("the unanswered approval answered %q", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	answer = f.awaiting(ctx, "bot", "toolu_2", time.Minute, &remembered)
	f.question(t)
	cancel()
	if got := received(t, answer); got != Withdrawn {
		t.Fatalf("the approval of a caller that left answered %q", got)
	}
	answer = f.awaiting(context.Background(), "bot", "toolu_3", time.Minute, &remembered)
	f.question(t)
	f.s.DeclineOwner("bot")
	if got := received(t, answer); got != Withdrawn {
		t.Fatalf("the approval of a deleted owner answered %q", got)
	}
	if len(f.endings) != 0 || len(f.s.load()) != 0 || len(f.broker.Questions()) != 0 || remembered != 0 {
		t.Fatalf("an unanswered approval was reported, outlived its end or remembered: %+v", f.endings)
	}
}

// A kind switched off is approved without a question.
func TestAnAwaitedApprovalOfASwitchedOffKindAsksNothing(t *testing.T) {
	f := newFixture(t)
	f.off["tool"] = true
	remembered := 0
	if got := received(t, f.awaiting(context.Background(), "bot", "toolu_1", time.Minute, &remembered)); got != Approved {
		t.Fatalf("the switched off kind answered %q", got)
	}
	if len(f.broker.Questions()) != 0 || len(f.s.load()) != 0 {
		t.Fatal("a switched off kind asked or wrote something down")
	}
}

// An awaited approval the last process left is taken down by the restart
// without a report: its caller heard of it when its request broke off.
func TestARestartTakesAnAwaitedApprovalDownQuietly(t *testing.T) {
	f := newFixture(t)
	remembered := 0
	f.awaiting(context.Background(), "bot", "toolu_1", time.Minute, &remembered)
	f.question(t)
	f.service().Recover()
	if len(f.endings) != 0 || len(f.s.load()) != 0 {
		t.Fatalf("the restart reported %+v or left the entry", f.endings)
	}
}
