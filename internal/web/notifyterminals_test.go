package web

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/shell"
	"github.com/marein/dev-cockpit/internal/tmux"
)

// newsServer is a server with the two lists the terminal filter asks and
// nothing else. Neither holds a session here, which is the point: what is not
// a coder and not a shell must not pass on its id alone.
func newsServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	projects := project.NewRepository(dir, nil)
	return &Server{
		notifier: notify.NewService(filepath.Join(dir, "notifications.json"), nil),
		shells:   shell.NewShells(config.Config{}, tmux.New(), projects, nil),
		projects: projects,
	}
}

// The Terminals mark means a terminal, so everything that only runs in one
// falls out of it: a compose action, a backup job, a standing git question,
// and the assistant, whose conversation is a session the coder lists hide and
// which wears its own mark on its own button. The bell still has all of them,
// which is why only this list is narrowed and Targets stays whole.
func TestTerminalTargetsDropsWhatIsNoTerminal(t *testing.T) {
	s := newsServer(t)
	ids := []string{
		notify.DockerTarget("app"),
		notify.BackupTarget,
		notify.GitPromptTarget("app"),
		// UUID shaped, the shape a coder session and an assistant
		// conversation share: the shape is not what decides.
		"ffffffff-ffff-4fff-8fff-ffffffffffff",
	}
	if got := s.terminalTargets(ids); len(got) != 0 {
		t.Fatalf("want no terminal among %v, got %v", ids, got)
	}
	if s.anyTerminalNews() {
		t.Fatal("an empty notification store already claims terminal news")
	}
	for _, id := range ids {
		s.notifier.Add(id)
	}
	if s.notifier.UnreadCount() != len(ids) {
		t.Fatalf("the bell wants all %d entries, it counts %d", len(ids), s.notifier.UnreadCount())
	}
	if s.anyTerminalNews() {
		t.Fatalf("a compose, backup, git question or assistant entry lights the Terminals mark: %v", s.terminalTargets(ids))
	}
}

// The stream hands the browser both lists in one payload: the whole one the
// bell counts and the narrowed one the Terminals dot reads. The field names
// are the contract notifications.js reads, so they are asserted on the wire.
func TestNotifyPayloadCarriesBothLists(t *testing.T) {
	s := newsServer(t)
	s.notifier.Add(notify.DockerTarget("app"))

	data, err := json.Marshal(s.notifyPayload(s.notifier.UnreadEvent()))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got struct {
		Unread    int      `json:"unread"`
		Targets   []string `json:"targets"`
		Terminals []string `json:"terminals"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal %s: %v", data, err)
	}
	if got.Unread != 1 || len(got.Targets) != 1 {
		t.Fatalf("the bell lost the compose entry: %s", data)
	}
	if got.Terminals == nil {
		t.Fatalf("the terminal list must be a list, never missing: %s", data)
	}
	if len(got.Terminals) != 0 {
		t.Fatalf("the compose entry reached the terminal list: %s", data)
	}
}
