package forge

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

// retryStub is a provider that reads status like pollStub and records what
// it was asked to run again.
type retryStub struct {
	*pollStub
	retried []string
	fail    error
}

func (r *retryStub) RetryPipeline(_ context.Context, _ *Client, b Binding, token, head string, p Pipeline) error {
	r.retried = append(r.retried, "pipeline "+p.ID+" at "+head+" of "+b.Path+" "+token)
	return r.fail
}

func (r *retryStub) RetryJob(_ context.Context, _ *Client, b Binding, token string, j Job) error {
	r.retried = append(r.retried, "job "+j.ID+" of "+b.Path+" "+token)
	return r.fail
}

func newRetryFixture(t *testing.T) (*pollFixture, *retryStub) {
	stub := &pollStub{answers: map[string]BranchStatus{}}
	retry := &retryStub{pollStub: stub}
	f := newPollFixtureOf(t, retry, stub)
	stub.answers["a1"] = BranchStatus{Pipeline: &Pipeline{ID: "12", Status: Failed, URL: "https://forge.example.com/p/12", Jobs: []Job{
		{ID: "30", Name: "build", Status: Success},
		{ID: "31", Name: "unit", Status: Failed},
		{ID: "33", Name: "ci/external", Status: Failed, NoRetry: "An external status cannot run again."},
		{ID: "34", Name: "lint", Status: Failed, AllowFailure: true},
	}}}
	return f, retry
}

// A retry reads the branch from the forge, runs the failed jobs of the
// pipeline that stands again, and has the poller read the branch at once and
// fast for a while, though the pipeline it read last had ended.
func TestARetryRunsTheFailedJobsOfThePipelineThatStands(t *testing.T) {
	f, stub := newRetryFixture(t)
	w := f.watch("app", "feature", "a1")
	f.round(w)
	reads := f.reads()
	target, err := f.s.RetryTarget(context.Background(), w, Retry{})
	if err != nil || target.Head != "a1" || target.Pipeline.ID != "12" || target.Job != nil || len(stub.retried) != 0 {
		t.Fatalf("the target reads %+v, %v, ran %q", target, err, stub.retried)
	}
	if got := target.Retry(); got != (Retry{Head: "a1", Pipeline: "12"}) {
		t.Fatalf("the target names %+v", got)
	}
	if left := target.Pipeline.LeftOut(); len(left) != 1 || left[0].ID != "33" {
		t.Fatalf("the jobs left as they are read %+v", left)
	}
	done, err := f.s.Retry(context.Background(), w, target.Retry())
	if err != nil || done.Pipeline.ID != "12" || done.Job != nil {
		t.Fatalf("the retry answered %+v, %v", done, err)
	}
	if want := []string{"pipeline 12 at a1 of group/app " + pollToken}; !slices.Equal(stub.retried, want) {
		t.Fatalf("the forge was asked %q", stub.retried)
	}
	if f.reads() != reads+2 {
		t.Fatalf("the target and the retry read the forge %d times, want each once", f.reads()-reads)
	}
	f.round(w)
	if f.reads() != reads+3 {
		t.Fatal("the branch was not read at once after the retry")
	}
	f.pass(pollFast)
	f.round(w)
	if f.reads() != reads+4 {
		t.Fatal("the branch of a retried pipeline that still reads failed was not read fast")
	}
}

// A retry of one job runs that job again, and a failed job allowed to fail
// is one too.
func TestARetryOfOneJobRunsThatJobAgain(t *testing.T) {
	f, stub := newRetryFixture(t)
	w := f.watch("app", "feature", "a1")
	for _, id := range []string{"31", "34"} {
		done, err := f.s.Retry(context.Background(), w, Retry{Head: "a1", Pipeline: "12", Job: id})
		if err != nil || done.Job == nil || done.Job.ID != id || done.Retry().Job != id {
			t.Fatalf("the retry of %s answered %+v, %v", id, done, err)
		}
	}
	if want := []string{"job 31 of group/app " + pollToken, "job 34 of group/app " + pollToken}; !slices.Equal(stub.retried, want) {
		t.Fatalf("the forge was asked %q", stub.retried)
	}
}

// What a retry names has to stand where the forge has it now, failed and
// able to run again, or nothing is asked of the forge.
func TestARetryIsRefusedWhereItsPipelineMovedOn(t *testing.T) {
	f, stub := newRetryFixture(t)
	stub.answers["b1"] = BranchStatus{Pipeline: &Pipeline{ID: "13", Status: Running}}
	stub.answers["c1"] = BranchStatus{Pipeline: &Pipeline{ID: "14", Status: Failed, Jobs: []Job{{ID: "41", Name: "scan", Status: Failed, NoRetry: "Only CodeQL runs it again."}}}}
	stub.answers["d1"] = BranchStatus{Pipeline: &Pipeline{Status: Failed}}
	for _, c := range []struct {
		head string
		r    Retry
		want string
	}{
		{"", Retry{}, `"feature" is not pushed, it has no pipeline to run again.`},
		{"a1", Retry{Head: "a0"}, `"feature" was pushed again and stands at a1 now, its pipeline is another one.`},
		{"e1", Retry{}, `"feature" has no pipeline at its pushed head e1.`},
		{"a1", Retry{Pipeline: "11"}, `The pipeline of "feature" is #12 now, not #11.`},
		{"d1", Retry{Pipeline: "11"}, "Stub numbers no pipelines, name none."},
		{"b1", Retry{}, `The pipeline of "feature" is running, not failed.`},
		{"d1", Retry{}, "No listed job failed the pipeline, none runs again."},
		{"c1", Retry{}, `No job that failed the pipeline can run again. "scan": Only CodeQL runs it again.`},
		{"a1", Retry{Job: "35"}, `The pipeline of "feature" has no job "35", a job that ran again stands under a new id.`},
		{"a1", Retry{Job: "30"}, `The job "build" is success, not failed.`},
		{"a1", Retry{Job: "33"}, "An external status cannot run again."},
	} {
		_, err := f.s.Retry(context.Background(), f.watch("app", "feature", c.head), c.r)
		var refused *RetryRefusal
		if !errors.As(err, &refused) || err.Error() != c.want {
			t.Fatalf("a retry of %+v at %q answered %v, want %q", c.r, c.head, err, c.want)
		}
	}
	if len(stub.retried) != 0 {
		t.Fatalf("a refused retry asked the forge %q", stub.retried)
	}
}

// A forge whose provider runs nothing again, and a host without a token,
// refuse a retry without a request; a forge that refuses answers its words,
// and the branch is read at once, part of the retry may have run.
func TestARetryNeedsAForgeThatRunsJobsAgainAndAToken(t *testing.T) {
	f := newPollFixture(t)
	w := f.watch("app", "feature", "a1")
	if _, err := f.s.RetryTarget(context.Background(), w, Retry{}); !errors.Is(err, ErrNoRetry) {
		t.Fatalf("a forge that runs nothing again answered %v", err)
	}
	g, stub := newRetryFixture(t)
	g.round(w)
	stub.fail = errors.New("The token may not write to the forge.")
	if _, err := g.s.Retry(context.Background(), w, Retry{}); err == nil || err.Error() != "The token may not write to the forge." {
		t.Fatalf("a refused write answered %v", err)
	}
	reads := g.reads()
	g.round(w)
	if g.reads() != reads+1 {
		t.Fatal("the branch was not read at once after a refused retry")
	}
	if err := g.s.ChangeToken(g.host.ID, "", true); err != nil {
		t.Fatal(err)
	}
	if _, err := g.s.Retry(context.Background(), w, Retry{}); !errors.Is(err, ErrNoToken) {
		t.Fatalf("a host without a token answered %v", err)
	}
	if len(stub.retried) != 1 {
		t.Fatalf("the forge was asked %q", stub.retried)
	}
}

// The poller reads a branch whose pipeline was retried at once, and only the
// branch it keeps.
func TestARetryOfAnotherBranchLeavesThePollersPace(t *testing.T) {
	f, _ := newRetryFixture(t)
	w := f.watch("app", "feature", "a1")
	f.round(w)
	f.s.poll.wrote(f.watch("app", "main", "a1"))
	reads := f.reads()
	f.pass(time.Second)
	f.round(w)
	if f.reads() != reads {
		t.Fatal("a retry of another branch had the kept one read again")
	}
}
