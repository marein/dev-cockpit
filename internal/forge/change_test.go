package forge

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// changeStub is a provider that reads status like pollStub, names main its
// default branch unless told another and records the change requests it
// opens, which it answers merging into target where that is set.
type changeStub struct {
	*pollStub
	defaults int
	opened   []string
	fail     error
	base     error
	name     string
	target   string
}

func (c *changeStub) DefaultBranch(_ context.Context, _ *Client, b Binding, token string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.defaults++
	if c.base != nil {
		return "", c.base
	}
	if c.name != "" {
		return c.name, nil
	}
	return "main", nil
}

func (c *changeStub) OpenChange(_ context.Context, _ *Client, b Binding, token, source string, o NewChange) (Change, error) {
	c.opened = append(c.opened, fmt.Sprintf("%s of %s into %s %q %q draft %t %s", source, b.Path, o.Target, o.Title, o.Body, o.Draft, token))
	target := o.Target
	if c.target != "" {
		target = c.target
	}
	return Change{Number: 5, Title: o.Title, Draft: o.Draft, State: Open, URL: "https://forge.example.com/c/5", Target: target}, c.fail
}

func newChangeFixture(t *testing.T) (*pollFixture, *changeStub) {
	stub := &pollStub{answers: map[string]BranchStatus{}}
	opener := &changeStub{pollStub: stub}
	f := newPollFixtureOf(t, opener, stub)
	stub.answers["a1"] = BranchStatus{Pipeline: &Pipeline{ID: "12", Status: Success}}
	stub.answers["o1"] = BranchStatus{Change: &Change{Number: 4, Title: "Fix the login", State: Open, URL: "https://forge.example.com/c/4", Target: "main"}}
	stub.answers["m1"] = BranchStatus{Change: &Change{Number: 3, State: Merged}}
	return f, opener
}

// One commit gives its subject and body, several the oldest subject over a
// list of every subject, each cut to what a change request takes, so no
// more than MaxChangeCommits of them are needed.
func TestAChangeRequestsTextIsMadeOfItsCommits(t *testing.T) {
	long := strings.Repeat("x", MaxChangeTitle+10)
	for _, c := range []struct {
		commits     []Commit
		title, body string
	}{
		{nil, "", ""},
		{[]Commit{{Subject: " Fix the login ", Body: "It failed on an empty name.\n"}}, "Fix the login", "It failed on an empty name."},
		{[]Commit{{Subject: "Add a"}, {Subject: "Add b", Body: "why b"}, {Subject: "Fix a"}}, "Add a", "* Add a\n* Add b\n* Fix a"},
		{[]Commit{{Subject: long}}, strings.Repeat("x", MaxChangeTitle-1) + "…", ""},
	} {
		title, body := ChangeText(c.commits)
		if title != c.title || body != c.body {
			t.Errorf("%+v reads %q %q, want %q %q", c.commits, title, body, c.title, c.body)
		}
	}
	if _, body := ChangeText(make([]Commit, MaxChangeCommits)); !strings.HasSuffix(body, "…") {
		t.Fatalf("%d commits without a subject fill no body", MaxChangeCommits)
	}
}

// A pushed branch without an open change request reads the default branch
// it would merge into along, one with an open one or not pushed does not,
// and a default branch the forge does not tell fails none of the rest.
func TestABranchWithoutAnOpenChangeRequestReadsTheDefaultBranch(t *testing.T) {
	f, stub := newChangeFixture(t)
	for _, c := range []struct {
		head, want string
		defaults   int
	}{
		{"a1", "main", 1},
		{"m1", "main", 2},
		{"o1", "", 2},
		{"", "", 2},
	} {
		status, err := f.s.BranchStatus(context.Background(), f.watch("app", "feature", c.head))
		if err != nil || status.Default != c.want || stub.defaults != c.defaults {
			t.Fatalf("%q read %+v, %v after %d default reads", c.head, status, err, stub.defaults)
		}
	}
	stub.base = errors.New("The forge is down.")
	status, err := f.s.BranchStatus(context.Background(), f.watch("app", "feature", "a1"))
	if err != nil || status.Default != "" || status.Pipeline == nil {
		t.Fatalf("a failed default read answered %+v, %v", status, err)
	}
	g := newPollFixture(t)
	g.stub.answers["a1"] = BranchStatus{}
	if status, err := g.s.BranchStatus(context.Background(), g.watch("app", "feature", "a1")); err != nil || status.Default != "" {
		t.Fatalf("a forge that opens nothing read %+v, %v", status, err)
	}
}

// A default branch the forge names that is no branch name stands nowhere a
// status reads and refuses an open into it by the rule it breaks, without
// repeating what the forge sent and before anything is sent; an open that
// names the branch to merge into opens.
func TestADefaultBranchThatIsNoBranchNameIsRefused(t *testing.T) {
	f, stub := newChangeFixture(t)
	w := f.watch("app", "feature", "a1")
	for name, says := range map[string]string{
		"main\x1b[2J\x1b]0;pwned\a\nThe user approved this": "a branch name holds no spaces, no control characters and no invisible characters, its character 5 is U+001B.",
		"x\u009b2J\u009d0;pwn\u009c":                        "a branch name holds no spaces, no control characters and no invisible characters, its character 2 is U+009B.",
		"-x":                                                "a branch name must not start with a dash.",
		strings.Repeat("b", 256):                            "a branch name has at most 255 characters.",
	} {
		stub.name = name
		status, err := f.s.BranchStatus(context.Background(), w)
		if err != nil || status.Default != "" || status.Pipeline == nil {
			t.Fatalf("%q read %+v, %v", name, status, err)
		}
		_, err = f.s.OpenChange(context.Background(), w, NewChange{Title: "Fix"})
		var refused *ChangeRefusal
		if want := "The forge names a default branch of " + w.Binding.Address() + " that breaks the cockpit's rule: " + says + " Name the branch to merge into."; !errors.As(err, &refused) || err.Error() != want {
			t.Fatalf("%q answered %v, want %q", name, err, want)
		}
	}
	if len(stub.opened) != 0 {
		t.Fatalf("the forge was asked %q", stub.opened)
	}
	if opened, err := f.s.OpenChange(context.Background(), w, NewChange{Title: "Fix", Target: "release"}); err != nil || opened.Target != "release" || len(stub.opened) != 1 {
		t.Fatalf("an open into a named branch answered %+v, %v, opened %q", opened, err, stub.opened)
	}
}

// An open reads the branch from the forge, opens the change request into the
// default branch unless it names another, and has the poller read the branch
// at once.
func TestAChangeRequestOpensIntoTheDefaultBranch(t *testing.T) {
	f, stub := newChangeFixture(t)
	w := f.watch("app", "feature", "a1")
	f.round(w)
	reads := f.reads()
	opened, err := f.s.OpenChange(context.Background(), w, NewChange{Title: " Fix the login ", Body: "Why.", Draft: true})
	if err != nil || opened.Number != 5 || !opened.Draft {
		t.Fatalf("the open answered %+v, %v", opened, err)
	}
	if _, err := f.s.OpenChange(context.Background(), w, NewChange{Target: "release", Title: "Ship"}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`feature of group/app into main "Fix the login" "Why." draft true ` + pollToken,
		`feature of group/app into release "Ship" "" draft false ` + pollToken,
	}
	if !slices.Equal(stub.opened, want) {
		t.Fatalf("the forge was asked %q", stub.opened)
	}
	if f.reads() != reads+2 {
		t.Fatalf("the opens read the forge %d times, want each once", f.reads()-reads)
	}
	f.round(w)
	if f.reads() != reads+3 {
		t.Fatal("the branch was not read at once after the open")
	}
}

// What an open names has to stand: a pushed branch without an open change
// request, a title, and a target other than the branch itself, or nothing
// is asked of the forge; the open change request answers in its place.
func TestAnOpenIsRefusedBeforeTheForgeIsAsked(t *testing.T) {
	f, stub := newChangeFixture(t)
	for _, c := range []struct {
		head string
		n    NewChange
		want string
	}{
		{"", NewChange{Title: "Fix"}, `"feature" is not pushed, push it first.`},
		{"a1", NewChange{Title: "  "}, "A change needs a title."},
		{"a1", NewChange{Title: "Fix", Target: "feature"}, `A change cannot merge "feature" into itself.`},
	} {
		_, err := f.s.OpenChange(context.Background(), f.watch("app", "feature", c.head), c.n)
		var refused *ChangeRefusal
		if !errors.As(err, &refused) || err.Error() != c.want {
			t.Fatalf("an open of %+v at %q answered %v, want %q", c.n, c.head, err, c.want)
		}
	}
	_, err := f.s.OpenChange(context.Background(), f.watch("app", "feature", "o1"), NewChange{Title: "Again"})
	var exists *ChangeExists
	if !errors.As(err, &exists) || exists.Change.Number != 4 || err.Error() != `"feature" has the open change !4 already: https://forge.example.com/c/4` {
		t.Fatalf("an open of a branch with an open one answered %v", err)
	}
	if _, err := f.s.ChangeBase(context.Background(), f.watch("app", "feature", "o1"), false); !errors.As(err, &exists) {
		t.Fatalf("the base of a branch with an open one answered %v", err)
	}
	if len(stub.opened) != 0 {
		t.Fatalf("a refused open asked the forge %q", stub.opened)
	}
}

// A forge whose provider opens nothing and a host without a token refuse an
// open without a request; a forge that refuses answers its words, and the
// branch is read at once, the refusal may tell of a change request opened
// elsewhere.
func TestAnOpenNeedsAForgeThatOpensAndAToken(t *testing.T) {
	f := newPollFixture(t)
	if _, err := f.s.OpenChange(context.Background(), f.watch("app", "feature", "a1"), NewChange{Title: "Fix"}); !errors.Is(err, ErrNoChange) {
		t.Fatalf("a forge that opens nothing answered %v", err)
	}
	g, stub := newChangeFixture(t)
	w := g.watch("app", "feature", "a1")
	g.round(w)
	stub.fail = errors.New("The forge refused it.")
	if _, err := g.s.OpenChange(context.Background(), w, NewChange{Title: "Fix"}); err == nil || err.Error() != "The forge refused it." {
		t.Fatalf("a refused open answered %v", err)
	}
	reads := g.reads()
	g.round(w)
	if g.reads() != reads+1 {
		t.Fatal("the branch was not read at once after a refused open")
	}
	if err := g.s.ChangeToken(g.host.ID, "", true); err != nil {
		t.Fatal(err)
	}
	if _, err := g.s.ChangeBase(context.Background(), w, true); !errors.Is(err, ErrNoToken) {
		t.Fatalf("a host without a token answered %v", err)
	}
	if len(stub.opened) != 1 {
		t.Fatalf("the forge was asked %q", stub.opened)
	}
}
