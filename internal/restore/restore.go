// Package restore brings the working set of terminals back after the tmux
// server died (host reboot, tmux kill-server): coders are resumed, shells are
// recreated. A snapshot of the live terminals is kept current in the state
// dir; the startup pass replays it when the terminal restore setting is on.
// A plain dev-cockpit restart leaves tmux untouched, then every recorded
// terminal is still running and the pass changes nothing.
package restore

import (
	"log"
	"slices"
	"sync"
	"time"

	"github.com/local/dev-cockpit/internal/coder"
	"github.com/local/dev-cockpit/internal/notify"
	"github.com/local/dev-cockpit/internal/shell"
	"github.com/local/dev-cockpit/internal/statefile"
	"github.com/local/dev-cockpit/internal/tmux"
)

// SettingKey is the settings store key that switches the startup restore on
// (value "on"). Unset or anything else means off. The snapshot file is
// written regardless of the setting, so enabling it acts on current data.
const SettingKey = "terminal-restore"

// Entry is one recorded terminal in the snapshot file.
type Entry struct {
	Kind  string `json:"kind"`            // "coder" or "shell"
	Coder string `json:"coder,omitempty"` // owning coder id, coders only
	ID    string `json:"id,omitempty"`    // coder resume key, or the shell session id recreated verbatim
	Name  string `json:"name"`            // display name; for coders the resumable store name wins
	CWD   string `json:"cwd"`             // start directory
	Pos   int    `json:"pos,omitempty"`   // @dc_tab_pos at snapshot time, 0 when unset
	Group string `json:"group,omitempty"` // @dc_tab_group at snapshot time, empty when ungrouped
	GPos  int    `json:"gpos,omitempty"`  // @dc_tab_gpos at snapshot time, 0 when unset
	GName string `json:"gname,omitempty"` // @dc_tab_gname at snapshot time, may be empty
}

// snapshot is the JSON shape of the state file.
type snapshot struct {
	Terminals []Entry `json:"terminals"`
}

// Service keeps the terminal snapshot current and replays it at startup.
// Safe for concurrent use.
type Service struct {
	path     string
	enabled  func() bool
	coders   []*coder.Manager
	shells   *shell.Shells
	tmux     *tmux.Client
	notifier *notify.Service

	mu   sync.Mutex
	last []Entry // last written entries, skips no-change rewrites
}

// New wires up the service. path is the snapshot file, enabled reads the
// setting on every call.
func New(path string, enabled func() bool, coders []*coder.Manager, shells *shell.Shells, t *tmux.Client, notifier *notify.Service) *Service {
	return &Service{path: path, enabled: enabled, coders: coders, shells: shells, tmux: t, notifier: notifier}
}

// Write rewrites the snapshot from the live terminals. Called on every
// terminal mutation plus the periodic refresh, since sessions can change
// behind dev-cockpit's back and a crash announces nothing, the file must
// always be near-current. An unchanged scan writes nothing.
func (s *Service) Write() {
	entries := s.scan()
	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.Equal(entries, s.last) {
		return
	}
	statefile.Save(s.path, 0o644, snapshot{Terminals: entries})
	s.last = entries
}

// RunPeriodic rewrites the snapshot every interval. Never returns, run it on
// a goroutine.
func (s *Service) RunPeriodic(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		s.Write()
	}
}

// RunStartup replays the snapshot when the setting is on: recorded coders
// that are not running are resumed if their id is still in the coder's
// resumable store, recorded shells that are not running are recreated in
// their directory under their recorded id, so links, open tabs, and
// notification entries keep resolving (skipped when the directory is gone).
// The recorded tab position is re-applied. A recreated shell is a fresh
// bash, its output is gone, so its notifications are marked read instead of
// ringing for nothing. Notification entries whose target resolves to
// nothing are pruned and the snapshot is rewritten from a fresh scan; both
// run on every startup, also with the setting off, so dead entries clean
// themselves up regardless of the restore.
func (s *Service) RunStartup() {
	if s.enabled() {
		var snap snapshot
		statefile.Load(s.path, &snap)
		if len(snap.Terminals) > 0 {
			s.replay(snap.Terminals)
		}
	}
	s.pruneNotifications()
	s.Write()
}

func (s *Service) replay(recorded []Entry) {
	managers := map[string]*coder.Manager{}
	runningCoders := map[string]bool{}
	for _, m := range s.coders {
		managers[m.ID()] = m
		for _, r := range m.Snapshot().Running {
			runningCoders[r.Identifier] = true
		}
	}
	runningShells := map[string]bool{}
	for _, sh := range s.shells.List() {
		runningShells[sh.Identifier] = true
	}
	for _, e := range recorded {
		switch e.Kind {
		case "coder":
			if runningCoders[e.ID] {
				continue
			}
			m := managers[e.Coder]
			if m == nil {
				continue
			}
			if _, err := m.ResolveResumable(e.ID); err != nil {
				continue
			}
			if _, err := m.Resume(e.ID); err != nil {
				log.Printf("terminal restore: resume %s coder %q: %v", e.Coder, e.Name, err)
				continue
			}
			s.applyTabPos(e.ID, e.Pos)
			s.applyTabGroup(e)
			log.Printf("terminal restore: resumed %s coder %q", e.Coder, e.Name)
		case "shell":
			if e.ID == "" || runningShells[e.ID] {
				continue
			}
			if _, err := s.shells.StartWithKey(e.CWD, e.Name, e.ID); err != nil {
				log.Printf("terminal restore: shell %q in %s: %v", e.Name, e.CWD, err)
				continue
			}
			s.applyTabPos(e.ID, e.Pos)
			s.applyTabGroup(e)
			s.notifier.MarkTargetRead(e.ID)
			log.Printf("terminal restore: recreated shell %q in %s", e.Name, e.CWD)
		}
	}
}

func (s *Service) applyTabPos(session string, pos int) {
	if pos < 1 {
		return
	}
	if err := s.tmux.SetTabPosition(session, pos); err != nil {
		log.Printf("terminal restore: tab position for %s: %v", session, err)
	}
}

func (s *Service) applyTabGroup(e Entry) {
	if e.Group == "" || e.GPos < 1 {
		return
	}
	if err := s.tmux.SetTabGroupEntry(e.ID, e.Group, e.GPos, e.GName); err != nil {
		log.Printf("terminal restore: tab group for %s: %v", e.ID, err)
	}
}

// pruneNotifications drops notification entries whose target stayed dead
// through the restore pass (a shell whose directory is gone, a coder without
// a store entry), their links would resolve to nothing forever.
func (s *Service) pruneNotifications() {
	valid := map[string]bool{}
	for _, m := range s.coders {
		snap := m.Snapshot()
		for _, r := range snap.Running {
			valid[r.Identifier] = true
		}
		for _, stored := range snap.Resumable {
			valid[stored.SessionID] = true
		}
	}
	for _, sh := range s.shells.List() {
		valid[sh.Identifier] = true
	}
	if removed := s.notifier.PruneTargets(valid); removed > 0 {
		log.Printf("terminal restore: pruned %d notification(s) without a live target", removed)
	}
}

func (s *Service) scan() []Entry {
	var entries []Entry
	for _, m := range s.coders {
		coderID := m.ID()
		for _, r := range m.Snapshot().Running {
			entries = append(entries, Entry{Kind: "coder", Coder: coderID, ID: r.Identifier, Name: r.Name, CWD: r.CWD, Pos: r.TabPos, Group: r.TabGroup, GPos: r.TabGroupPos, GName: r.TabGroupName})
		}
	}
	for _, sh := range s.shells.List() {
		entries = append(entries, Entry{Kind: "shell", ID: sh.Identifier, Name: sh.Name, CWD: sh.CWD, Pos: sh.TabPos, Group: sh.TabGroup, GPos: sh.TabGroupPos, GName: sh.TabGroupName})
	}
	return entries
}
