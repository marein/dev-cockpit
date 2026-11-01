package forge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// offSentence is why the forge features are off for a branch, what names it
// breaking the rule at its character at.
func offSentence(what string, at int, code string) string {
	return fmt.Sprintf("The forge features are off for this branch, since %s breaks the cockpit's rule: a branch name holds no spaces, no control characters and no invisible characters, its character %d is U+%s.", what, at, code)
}

// What could hide in a line or rewrite a terminal is refused, by the
// character, every other name git takes stays allowed, emoji sequences made
// with joiners, selectors and tags among them.
func TestBranchRefusalRefusesWhatHidesInALine(t *testing.T) {
	hidden := func(at int, code string) string {
		return fmt.Sprintf("A branch name holds no spaces, no control characters and no invisible characters, its character %d is U+%s.", at, code)
	}
	for name, want := range map[string]string{
		"main":                 "",
		"feature/login.form":   "",
		"fix/umlaute-äöü":      "",
		"rocket-🚀":             "",
		"private-\ue000":       "",
		"dev-👩\u200d💻":         "",
		"dev-👨🏽\u200d💻":        "",
		"pride-🏳\ufe0f\u200d🌈": "",
		"love-❤\ufe0f":         "",
		"key-1\ufe0f\u20e3":    "",
		"wales-🏴\U000E0067\U000E0062\U000E0077\U000E006C\U000E0073\U000E007F": "",
		"x\x00":                    hidden(2, "0000"),
		"x\x1b[2J":                 hidden(2, "001B"),
		"x\x7f":                    hidden(2, "007F"),
		"x\u0085":                  hidden(2, "0085"),
		"x\u009b2J\u009d0;p\u009c": hidden(2, "009B"),
		"x\u202egnp.exe":           hidden(2, "202E"),
		"x\u200by":                 hidden(2, "200B"),
		"x\u00ady":                 hidden(2, "00AD"),
		"\ufeffmain":               hidden(1, "FEFF"),
		"x\u2066y\u2069":           hidden(2, "2066"),
		"x y":                      hidden(2, "0020"),
		"x\u2028y":                 hidden(2, "2028"),
		"main\u3164":               hidden(5, "3164"),
		"main\uffa0":               hidden(5, "FFA0"),
		"\u115fmain":               hidden(1, "115F"),
		"ma\u1160in":               hidden(3, "1160"),
		"ma\u034fin":               hidden(3, "034F"),
		"main\u2800":               hidden(5, "2800"),
		"main\ufe0f":               hidden(5, "FE0F"),
		"main\ufe00":               hidden(5, "FE00"),
		"main\U000E0100":           hidden(5, "E0100"),
		"ma\u200din":               hidden(3, "200D"),
		"🚀\u200d":                  hidden(2, "200D"),
		"x\U000E0067\U000E007F":    hidden(2, "E0067"),
		"🏴\U000E0067":              hidden(2, "E0067"),
		"x\x9by":                   "A branch name is valid UTF-8.",
		"-x":                       "A branch name must not start with a dash.",
		strings.Repeat("ü", 256):   "A branch name has at most 255 characters.",
	} {
		if got := BranchRefusal(name); got != want {
			t.Errorf("BranchRefusal(%q) = %q, want %q", name, got, want)
		}
	}
}

// A branch whose name or name on origin the gate refuses is never read: the
// poller holds why the forge features are off for it and tells the review
// threads of the branch before as gone, and every read and write of it is
// refused before the forge is asked.
func TestABranchTheForgeFeaturesAreOffForIsNeverAsked(t *testing.T) {
	f := newReviewFixture(t, true)
	f.stub.answer("a1", Running)
	f.stub.set(noted("fix this"), nil)
	f.round(f.watch("app", "feature", "a1"))
	if heard := f.heard(); len(heard) != 1 || heard[0].Change == nil {
		t.Fatalf("the branch before told %+v", heard)
	}
	reads := f.reads()
	off := f.watch("app", "x\u202egnp", "a1")
	f.round(off)
	if f.reads() != reads {
		t.Fatalf("the forge was asked %q", f.pollFixture.stub.asked()[reads:])
	}
	polled, ok := f.s.Polled("app", off.Binding, off.Branch)
	var refused *BranchOff
	if !ok || !errors.As(polled.Err, &refused) || polled.Err.Error() != offSentence("its name", 2, "202E") || polled.Status.Change != nil {
		t.Fatalf("the poller holds %+v, %v", polled, ok)
	}
	if heard := f.heard(); len(heard) != 1 || heard[0].Change != nil || len(heard[0].Review.Diff) != 0 {
		t.Fatalf("the threads of the branch before were not told gone: %+v", heard)
	}
	f.pass(pollSlow)
	f.round(off)
	if f.reads() != reads || len(f.heard()) != 0 {
		t.Fatal("a later look at the branch asked the forge or told the threads again")
	}

	tracking := f.watch("app", "feature", "a1")
	tracking.Remote = "y\u009b2J"
	for want, w := range map[string]Watch{offSentence("its name", 2, "202E"): off, offSentence("its name on origin", 2, "009B"): tracking} {
		if _, err := f.s.BranchStatus(context.Background(), w); err == nil || err.Error() != want {
			t.Fatalf("a read of %q answered %v", w.Branch, err)
		}
		if _, err := f.s.Status(context.Background(), w, true); err == nil || err.Error() != want {
			t.Fatalf("a fresh read of %q answered %v", w.Branch, err)
		}
	}
	if f.reads() != reads {
		t.Fatalf("the forge was asked %q", f.pollFixture.stub.asked()[reads:])
	}
}

// Every write reads the branch first, so the gate refuses it before the
// forge is asked to write anything.
func TestAWriteOfABranchTheForgeFeaturesAreOffForAsksNothing(t *testing.T) {
	c, opener := newChangeFixture(t)
	off := c.watch("app", "x\u009d0;pwn\u009c", "a1")
	var refused *BranchOff
	if _, err := c.s.ChangeBase(context.Background(), off, true); !errors.As(err, &refused) {
		t.Fatalf("the base answered %v", err)
	}
	if _, err := c.s.OpenChange(context.Background(), off, NewChange{Title: "Fix", Target: "main"}); !errors.As(err, &refused) {
		t.Fatalf("the open answered %v", err)
	}
	if c.reads() != 0 || opener.defaults != 0 || len(opener.opened) != 0 {
		t.Fatalf("the forge was asked %q, %d defaults, opened %q", c.stub.asked(), opener.defaults, opener.opened)
	}

	r, retrier := newRetryFixture(t)
	off = r.watch("app", "x\u202e", "a1")
	if _, err := r.s.RetryTarget(context.Background(), off, Retry{}); !errors.As(err, &refused) {
		t.Fatalf("the retry target answered %v", err)
	}
	if _, err := r.s.Retry(context.Background(), off, Retry{}); !errors.As(err, &refused) {
		t.Fatalf("the retry answered %v", err)
	}
	if r.reads() != 0 || len(retrier.retried) != 0 {
		t.Fatalf("the forge was asked %q, ran %q", r.stub.asked(), retrier.retried)
	}
}

// A change request the forge answers merging into a branch the gate refuses
// switches the forge features off for its source branch where it is read;
// one just opened merges into the branch the open named, whatever the forge
// answers.
func TestAChangeRequestIntoARefusedBranchSwitchesTheForgeOff(t *testing.T) {
	f, opener := newChangeFixture(t)
	want := offSentence("the branch its change !4 merges into", 4, "009B")
	f.stub.answers["o1"] = BranchStatus{Change: &Change{Number: 4, State: Open, Target: "main"}, Pipeline: &Pipeline{ID: "12", Status: Success}}
	f.round(f.watch("app", "feature", "o1"))
	if polled, ok := f.s.Polled("app", f.watch("app", "feature", "o1").Binding, "feature"); !ok || polled.Status.Change == nil {
		t.Fatalf("the poller holds %+v, %v", polled, ok)
	}
	f.stub.answers["o1"] = BranchStatus{Change: &Change{Number: 4, State: Open, Target: "rel\u009b2J"}, Pipeline: &Pipeline{ID: "12", Status: Success}}
	status, err := f.s.BranchStatus(context.Background(), f.watch("app", "feature", "o1"))
	if err == nil || err.Error() != want || status.Change != nil || status.Pipeline != nil {
		t.Fatalf("the read answered %+v, %v", status, err)
	}
	f.pass(pollSlow)
	f.round(f.watch("app", "feature", "o1"))
	if polled, ok := f.s.Polled("app", f.watch("app", "feature", "o1").Binding, "feature"); !ok || polled.Err == nil || polled.Err.Error() != want || polled.Status.Change != nil || polled.Status.Pipeline != nil {
		t.Fatalf("the poller holds %+v after the target moved onto a refused branch", polled)
	}

	f.stub.answers["a1"] = BranchStatus{Pipeline: &Pipeline{ID: "12", Status: Success}}
	opener.target = "rel\u200b"
	opened, err := f.s.OpenChange(context.Background(), f.watch("app", "feature", "a1"), NewChange{Title: "Fix", Target: "main"})
	if err != nil || opened.Number != 5 || opened.Target != "main" {
		t.Fatalf("an open the forge answers into another branch answered %+v, %v", opened, err)
	}
	if len(opener.opened) != 1 {
		t.Fatalf("the forge opened %q", opener.opened)
	}
}
