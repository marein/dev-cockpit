package forge

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// GitLab's diff of a file where line 10 changed and a line came after line
// 15, as GitLab 19.5 answers it: a context line keeps its old line, an added
// one has none, a removed one stands on no line of the new side.
func TestHunkLinesReadEachLineOfTheNewSideWithItsOldOne(t *testing.T) {
	patch := "@@ -7,12 +7,13 @@ line6\n line7\n line8\n line9\n-line10\n+line10 changed\n line11\n line12\n line13\n line14\n line15\n+added after 15\n line16\n line17\n line18\n"
	want := map[int]int{7: 7, 8: 8, 9: 9, 10: 0, 11: 11, 12: 12, 13: 13, 14: 14, 15: 15, 16: 0, 17: 16, 18: 17, 19: 18}
	if got := HunkLines(patch); !maps.Equal(got, want) {
		t.Fatalf("the lines read %v", got)
	}
	for _, c := range []struct {
		name, patch string
		want        map[int]int
	}{
		{"a new file", "@@ -0,0 +1,2 @@\n+a\n+b", map[int]int{1: 0, 2: 0}},
		{"two hunks, one count left out", "@@ -1 +1 @@\n-a\n+b\n@@ -20,2 +20,2 @@ func x() {\n c\n d\n\\ No newline at end of file", map[int]int{1: 0, 20: 20, 21: 21}},
		{"a context line that lost its blank", "@@ -3,3 +3,3 @@\n x\n\n z\n", map[int]int{3: 3, 4: 4, 5: 5}},
		{"no hunk at all", "Binary files differ", map[int]int{}},
		{"a head that cannot be read", "@@ -a,b +c,d @@\n x\n", map[int]int{}},
	} {
		if got := HunkLines(c.patch); !maps.Equal(got, c.want) {
			t.Errorf("%s reads %v", c.name, got)
		}
	}
}

// publishStub reads status like pollStub and review threads like reviewStub,
// answers diff as the diff of every change request and posts each note it is
// asked to, the number it fails at refused.
type publishStub struct {
	*reviewStub
	mu       sync.Mutex
	diff     Diff
	diffs    int
	posted   []string
	failAt   int
	diffFail error
}

func (p *publishStub) ChangeDiff(_ context.Context, _ *Client, b Binding, token string, ch Change) (Diff, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.diffs++
	return p.diff, p.diffFail
}

func (p *publishStub) PublishNote(_ context.Context, _ *Client, b Binding, token string, ch Change, d Diff, pl Placed) (Note, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.posted)+1 == p.failAt {
		return Note{}, errors.New("The forge refused it.")
	}
	p.posted = append(p.posted, b.Path+" !"+strconv.Itoa(ch.Number)+" "+pl.Path+":"+strconv.Itoa(pl.Line)+" old "+pl.OldPath+":"+strconv.Itoa(pl.OldLine)+" at "+d.Head+" "+token+": "+pl.Body)
	id := strconv.Itoa(100 + len(p.posted))
	return Note{ID: id, Author: "root", Body: pl.Body, URL: ch.URL + "#note_" + id}, nil
}

func (p *publishStub) PublishGeneral(_ context.Context, _ *Client, b Binding, token string, ch Change, body string) (Note, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.posted)+1 == p.failAt {
		return Note{}, errors.New("The forge refused it.")
	}
	p.posted = append(p.posted, b.Path+" !"+strconv.Itoa(ch.Number)+" general "+token+": "+body)
	id := strconv.Itoa(100 + len(p.posted))
	return Note{ID: id, Author: "root", Body: body, URL: ch.URL + "#note_" + id}, nil
}

func (p *publishStub) asked() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.posted)
}

type publishFixture struct {
	*reviewFixture
	stub *publishStub
}

func newPublishFixture(t *testing.T) *publishFixture {
	review := &reviewStub{pollStub: &pollStub{answers: map[string]BranchStatus{}}}
	stub := &publishStub{reviewStub: review, diff: Diff{Head: "a1", Base: "b0", Start: "b0", Files: map[string]DiffFile{
		"src/login.go": {OldPath: "src/login.go", Lines: map[int]int{3: 3, 4: 0, 5: 4}},
		"src/new.go":   {OldPath: "src/old.go", Lines: map[int]int{1: 1}},
	}}}
	f := &publishFixture{reviewFixture: &reviewFixture{pollFixture: newPollFixtureOf(t, stub, review.pollStub), stub: review}, stub: stub}
	f.s.OnReview(func(r ReviewRead) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.got = append(f.got, r)
	}, func(map[string]bool) {})
	review.answers["a1"] = BranchStatus{Change: &Change{Number: 4, State: Open, URL: "https://forge.example.com/group/app/-/merge_requests/4", Head: "a1"}}
	return f
}

// A publish reads the branch and the diff of its change request afresh,
// places each draft on its line there, and posts them in order, a line both
// sides share with its old line, one the change adds without.
func TestAPublishPostsEachDraftOnItsLineOfTheDiff(t *testing.T) {
	f := newPublishFixture(t)
	w := f.watch("app", "feature", "a1")
	drafts := []Draft{{Path: "src/login.go", Line: 4, Body: "Why this?"}, {Path: "src/new.go", Line: 1, Body: "Renamed?"}}
	target, err := f.s.PublishTarget(context.Background(), w, drafts)
	if err != nil || target.Change.Number != 4 || len(f.stub.asked()) != 0 {
		t.Fatalf("the target reads %+v, %v, posted %q", target, err, f.stub.asked())
	}
	want := []Placed{{Draft: drafts[0], OldPath: "src/login.go"}, {Draft: drafts[1], OldPath: "src/old.go", OldLine: 1}}
	if !reflect.DeepEqual(target.Placed, want) {
		t.Fatalf("the drafts are placed %+v", target.Placed)
	}
	reads := f.reads()
	done, notes, err := f.s.Publish(context.Background(), w, drafts)
	if err != nil || done.Change.Number != 4 || len(notes) != 2 || notes[1].URL != "https://forge.example.com/group/app/-/merge_requests/4#note_102" {
		t.Fatalf("the publish answered %+v, %+v, %v", done, notes, err)
	}
	posted := []string{
		"group/app !4 src/login.go:4 old src/login.go:0 at a1 " + pollToken + ": Why this?",
		"group/app !4 src/new.go:1 old src/old.go:1 at a1 " + pollToken + ": Renamed?",
	}
	if !slices.Equal(f.stub.asked(), posted) || f.reads() != reads+1 {
		t.Fatalf("the forge was asked %q, read %d times", f.stub.asked(), f.reads()-reads)
	}
}

// Nothing is posted where the branch is not pushed, has no open change
// request, the forge shows the change request at another commit, or a draft
// stands on no line of its diff; every draft of a publish goes, or none.
func TestAPublishIsRefusedBeforeAnythingIsPosted(t *testing.T) {
	f := newPublishFixture(t)
	f.stub.answers["m1"] = BranchStatus{Change: &Change{Number: 3, State: Merged}}
	f.stub.answers["c1"] = BranchStatus{}
	f.stub.answers["d1"] = f.stub.answers["a1"]
	in := Draft{Path: "src/login.go", Line: 3, Body: "ok"}
	out := func(line int) Draft { return Draft{Path: "src/login.go", Line: line, Body: "far"} }
	for _, c := range []struct {
		head   string
		drafts []Draft
		want   string
	}{
		{"", []Draft{in}, `"feature" is not pushed, it has no change to publish comments on.`},
		{"m1", []Draft{in}, `"feature" has no open change to publish comments on.`},
		{"c1", []Draft{in}, `"feature" has no open change to publish comments on.`},
		{"d1", []Draft{in}, `The change !4 stands at a1 on Stub, not at the pushed head d1 of "feature": a fetch or a push catches up.`},
		{"a1", []Draft{out(9)}, `"src/login.go":9 stands on no line the diff of change !4 shows, nothing was published.`},
		{"a1", []Draft{in, {Path: "README.md", Line: 1}, out(1)}, `2 of the 3 comments stand on no line the diff of change !4 shows, nothing was published: "README.md":1 and "src/login.go":1.`},
		{"a1", []Draft{out(1), out(2), out(6), out(7), out(8)}, `5 of the 5 comments stand on no line the diff of change !4 shows, nothing was published: "src/login.go":1, "src/login.go":2, "src/login.go":6 and 2 more.`},
	} {
		_, notes, err := f.s.Publish(context.Background(), f.watch("app", "feature", c.head), c.drafts)
		var refused *PublishRefusal
		if !errors.As(err, &refused) || err.Error() != c.want || len(notes) != 0 {
			t.Errorf("at %q %v answered %v, %v", c.head, c.drafts, notes, err)
		}
	}
	f.stub.diff.Partial = true
	_, _, err := f.s.Publish(context.Background(), f.watch("app", "feature", "a1"), []Draft{out(9)})
	if err == nil || err.Error() != `"src/login.go":9 stands on no line the diff of change !4 shows, nothing was published. Stub lists more files of it than one read takes, a file past them counts as outside.` {
		t.Fatalf("outside a partial diff answered %v", err)
	}
	if len(f.stub.asked()) != 0 {
		t.Fatalf("a refused publish posted %q", f.stub.asked())
	}
}

// A refusal of the forge ends the publish there: the notes posted before it
// are answered with it, and the branch is read again at once.
func TestAPublishTheForgeRefusesHalfwayAnswersWhatItPosted(t *testing.T) {
	f := newPublishFixture(t)
	w := f.watch("app", "feature", "a1")
	f.round(w)
	f.stub.failAt = 2
	_, notes, err := f.s.Publish(context.Background(), w, []Draft{{Path: "src/login.go", Line: 3, Body: "one"}, {Path: "src/login.go", Line: 4, Body: "two"}, {Path: "src/login.go", Line: 5, Body: "three"}})
	if err == nil || err.Error() != "The forge refused it." || len(notes) != 1 || notes[0].Body != "one" || len(f.stub.asked()) != 1 {
		t.Fatalf("the publish answered %+v, %v, posted %q", notes, err, f.stub.asked())
	}
	reads := f.reads()
	f.round(w)
	if f.reads() != reads+1 {
		t.Fatal("the branch was not read at once after a half refused publish")
	}
}

// A general draft posts as a general comment in its turn among the others,
// on no line of the diff, and a publish of general drafts alone reads no diff,
// so a change request the forge shows at another commit takes them too.
func TestAPublishPostsAGeneralDraftAsAGeneralComment(t *testing.T) {
	f := newPublishFixture(t)
	drafts := []Draft{{General: true, Body: "Ready?"}, {Path: "src/login.go", Line: 4, Body: "Why this?"}}
	done, notes, err := f.s.Publish(context.Background(), f.watch("app", "feature", "a1"), drafts)
	if err != nil || len(notes) != 2 || !reflect.DeepEqual(done.Placed[0], Placed{Draft: drafts[0]}) {
		t.Fatalf("the publish answered %+v, %+v, %v", done, notes, err)
	}
	posted := []string{"group/app !4 general " + pollToken + ": Ready?", "group/app !4 src/login.go:4 old src/login.go:0 at a1 " + pollToken + ": Why this?"}
	if !slices.Equal(f.stub.asked(), posted) || f.stub.diffs != 1 {
		t.Fatalf("the forge was asked %q after %d diff reads", f.stub.asked(), f.stub.diffs)
	}
	f.stub.answers["d1"] = f.stub.answers["a1"]
	if _, notes, err := f.s.Publish(context.Background(), f.watch("app", "feature", "d1"), drafts[:1]); err != nil || len(notes) != 1 || f.stub.diffs != 1 {
		t.Fatalf("general drafts alone answered %+v, %v after %d diff reads", notes, err, f.stub.diffs)
	}
	if _, _, err := f.s.Publish(context.Background(), f.watch("app", "feature", "d1"), drafts); err == nil || !strings.Contains(err.Error(), "not at the pushed head d1") || len(f.stub.asked()) != 3 {
		t.Fatalf("a general draft beside one on a line of a diff at another commit answered %v, posted %q", err, f.stub.asked())
	}
}

// A publish has the poller read the branch's review threads at once, though
// it read them less than a minute ago, and a read of them that was in flight
// when the publish posted tells nothing, so it does not take back the notes
// the publish made.
func TestAPublishHasTheThreadsReadAgainAndDropsAReadInFlight(t *testing.T) {
	f := newPublishFixture(t)
	w := f.watch("app", "feature", "a1")
	f.stub.set(Review{Diff: []Thread{{ID: "t1", Notes: []Note{{ID: "1"}}}}}, nil)
	f.round(w)
	if got := f.heard(); len(got) != 1 || f.stub.threadReads() != 1 {
		t.Fatalf("the first read told %+v after %d thread reads", got, f.stub.threadReads())
	}
	gate := make(chan struct{})
	f.stub.mu.Lock()
	f.stub.gate = gate
	f.stub.mu.Unlock()
	f.pass(pollSlow)
	f.start(w)
	f.stub.set(Review{Diff: []Thread{{ID: "t1", Notes: []Note{{ID: "1"}, {ID: "2"}}}}}, nil)
	f.s.poll.wroteNotes(w)
	close(gate)
	f.s.poll.reads.Wait()
	f.stub.mu.Lock()
	f.stub.gate = nil
	f.stub.mu.Unlock()
	if got := f.heard(); len(got) != 0 {
		t.Fatalf("a read in flight across a publish told %+v", got)
	}
	f.pass(pollFast)
	f.round(w)
	got := f.heard()
	if len(got) != 1 || len(got[0].Review.Diff[0].Notes) != 2 {
		t.Fatalf("the read after the publish told %+v", got)
	}
}

// A provider that cannot publish says so, a host without a token too, and
// neither reads the forge.
func TestAPublishNeedsAProviderThatPublishesAndAToken(t *testing.T) {
	f := newPollFixture(t)
	w := f.watch("app", "feature", "a1")
	if _, err := f.s.PublishTarget(context.Background(), w, nil); !errors.Is(err, ErrNoPublish) || f.s.Publishes(w.Binding) {
		t.Fatalf("a provider without the capability answered %v", err)
	}
	p := newPublishFixture(t)
	if !p.s.Publishes(w.Binding) {
		t.Fatal("a provider that publishes is said not to")
	}
	if err := p.s.ChangeToken(p.host.ID, "", true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := p.s.Publish(context.Background(), p.watch("app", "feature", "a1"), nil); !errors.Is(err, ErrNoToken) || p.reads() != 0 {
		t.Fatalf("a host without a token answered %v after %d reads", err, p.reads())
	}
}
