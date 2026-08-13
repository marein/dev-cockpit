package web

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/local/dev-cockpit/internal/askpass"
	"github.com/local/dev-cockpit/internal/eventbus"
	"github.com/local/dev-cockpit/internal/notify"
)

// promptNewsServer is a server with nothing but the two things this path
// needs, plus a bridge that really listens: the helper side blocks on the
// socket, so a question is only standing once a helper is waiting on it.
func promptNewsServer(t *testing.T) (*Server, *askpass.Broker, string) {
	t.Helper()
	dir := t.TempDir()
	broker := askpass.New(dir)
	listener, err := askpass.Listen(dir)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() { _ = http.Serve(listener, broker.Handler()) }()
	s := &Server{
		notifier: notify.NewService(filepath.Join(dir, "notifications.json"), nil),
		bus:      eventbus.New(),
	}
	return s, broker, askpass.SocketPath(dir)
}

// tokenOf reads the one-time token out of the environment an action hands its
// helpers, which is the only exported way to it.
func tokenOf(t *testing.T, action *askpass.Action) string {
	t.Helper()
	for _, entry := range action.Env() {
		if value, ok := strings.CutPrefix(entry, "DC_ASKPASS_TOKEN="); ok {
			return value
		}
	}
	t.Fatal("the action carries no token")
	return ""
}

// A question has to reach somebody with no page open at all, so it is news
// like a coder's: unread while it stands, read again the moment it is gone,
// however it went. Without the second half the bell would claim a question
// nobody can answer any more.
func TestGitPromptNewsFollowsTheStandingQuestions(t *testing.T) {
	s, broker, socket := promptNewsServer(t)
	s.SetAskpass(broker, "/nowhere/helper")

	// A proxied call, in through the route's own helper: nobody is looking at
	// a page, so this is the kind that has to leave the app.
	action, _, open := s.promptActionCommand("demo", "push", "git push", "/srv/projects/demo")
	if !open || action == nil {
		t.Fatal("the bridge must open")
	}
	go func() { _, _ = askpass.Ask(socket, tokenOf(t, action), "Enter passphrase for key '/zz/k':") }()

	target := notify.GitPromptTarget("demo")
	waitFor(t, "the standing question becomes unread news", func() bool {
		return s.notifier.UnreadTargets()[target]
	})

	// The action ends the way a cancelled or a timed out one does: the
	// question goes with it, and the entry has to follow.
	action.End()
	waitFor(t, "the entry is read again once the question is gone", func() bool {
		return !s.notifier.UnreadTargets()[target]
	})
}

// The other kind of asker: an action started on a page in the app. Somebody
// is looking at that page and it is showing the dialog, so news about it
// would ring for something the person already has in front of them, and the
// push channels would carry it to a phone for nothing.
//
// It goes in through the editor's own helper and not through the broker,
// which is the only way this proves anything: the two kinds are told apart at
// that layer, and a helper routing both through one constructor puts every
// editor push on the push channels while a test asking the broker directly
// stays green.
func TestGitPromptNewsIgnoresTheEditorsOwnQuestions(t *testing.T) {
	s, broker, socket := promptNewsServer(t)
	s.SetAskpass(broker, "/nowhere/helper")

	action, _, open := s.promptAction("demo", "push")
	if !open || action == nil {
		t.Fatal("the bridge must open")
	}
	go func() { _, _ = askpass.Ask(socket, tokenOf(t, action), "Enter passphrase for key '/zz/k':") }()

	// The question really stands, so the check below is about the news and
	// not about a question that never arrived.
	waitFor(t, "the question stands", func() bool { return len(broker.Questions()) == 1 })
	// Give the change hook the room it would need to write an entry.
	time.Sleep(300 * time.Millisecond)
	if s.notifier.UnreadTargets()[notify.GitPromptTarget("demo")] {
		t.Fatal("an action started in the app must not become news")
	}
	action.End()
}

// Which question holds a notification entry is this server's rule, so the
// server hands the target out with the question instead of leaving the dialog
// to work the prefix out a second time in another language.
func TestGitPromptViewsCarryTheTargetOfTheQuestionsThatHaveOne(t *testing.T) {
	s, broker, socket := promptNewsServer(t)
	s.SetAskpass(broker, "/nowhere/helper")

	proxied, _, open := s.promptActionCommand("demo", "push", "git push", "/srv/projects/demo")
	if !open || proxied == nil {
		t.Fatal("the bridge must open")
	}
	defer proxied.End()
	go func() { _, _ = askpass.Ask(socket, tokenOf(t, proxied), "Enter passphrase for key '/zz/k':") }()
	waitFor(t, "the proxied question stands", func() bool { return len(s.gitPromptViews()) == 1 })
	if got := s.gitPromptViews()[0].Target; got != notify.GitPromptTarget("demo") {
		t.Fatalf("the proxied question carries the target %q", got)
	}

	own, _, ok := s.promptAction("other", "push")
	if !ok || own == nil {
		t.Fatal("the second bridge must open")
	}
	defer own.End()
	go func() { _, _ = askpass.Ask(socket, tokenOf(t, own), "Enter passphrase for key '/zz/k':") }()
	waitFor(t, "the editor's question stands", func() bool { return len(s.gitPromptViews()) == 2 })
	for _, view := range s.gitPromptViews() {
		if view.Project == "other" && view.Target != "" {
			t.Fatalf("an action started on a page must hold no entry, got %q", view.Target)
		}
	}
}

// The broker's questions live in memory, so after a restart none stands. An
// entry a killed process left unread would claim a question forever, which is
// what the boot sweep in SetAskpass is for.
func TestSetAskpassClearsStaleQuestionNews(t *testing.T) {
	s, broker, _ := promptNewsServer(t)
	target := notify.GitPromptTarget("demo")
	s.notifier.Add(target)
	if !s.notifier.UnreadTargets()[target] {
		t.Fatal("the stale entry must start unread")
	}

	s.SetAskpass(broker, "/nowhere/helper")

	if s.notifier.UnreadTargets()[target] {
		t.Fatal("a restart must not leave a question nobody can answer standing in the bell")
	}
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting until %s", what)
}
