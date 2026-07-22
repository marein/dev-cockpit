package shell

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/local/dev-cockpit/internal/config"
	"github.com/local/dev-cockpit/internal/project"
	"github.com/local/dev-cockpit/internal/terminal"
	"github.com/local/dev-cockpit/internal/tmux"
)

// HistorySettingKey is the settings store key that switches per-shell command
// history on (value "on"). Unset or anything else means off: shells then share
// the login shell's default history file. It only affects newly started
// shells, like the terminal restore setting.
const HistorySettingKey = "shell-history"

// shellNameOption is the tmux user option that holds a shell's display name. Its
// presence marks a tmux session as a shell and tells it apart from coder
// sessions. It is mutable at runtime, so shells can be renamed in place.
const shellNameOption = "@dc_shell_name"

// shellDirOption is the tmux user option that records a shell's start directory.
// It lets the UI group shells under the project they were launched in. Coder
// sessions and shells created before this option existed simply lack it.
const shellDirOption = "@dc_shell_dir"

// maxShellNameLength bounds a shell's display name.
const maxShellNameLength = 120

// ErrNotRunning marks lookups for identifiers without a live shell.
var ErrNotRunning = errors.New("No active shell")

// Shell is one live tmux session running a plain interactive login shell.
type Shell struct {
	Identifier   string
	TmuxSession  string
	PID          string
	Name         string
	StartedAt    time.Time
	CWD          string
	TabPos       int    // tab strip position from @dc_tab_pos, 0 when unset
	TabGroup     string // split view group id from @dc_tab_group, empty when ungrouped
	TabGroupPos  int    // position inside the group from @dc_tab_gpos, 0 when unset
	TabGroupName string // group display name from @dc_tab_gname, may be empty
}

// shellsCache memoises List for a short TTL so the repeated tmux pane scans
// during one page render (the quick nav's Active list plus its per-project
// browser, and the projects page) collapse to a single scan. Mirrors the session
// snapshot cache and is invalidated on every shell mutation.
type shellsCache struct {
	mu      sync.Mutex
	value   []Shell
	ready   bool
	expires time.Time
	ttl     time.Duration
}

func (c *shellsCache) get() ([]Shell, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ready && time.Now().Before(c.expires) {
		return c.value, true
	}
	return nil, false
}

func (c *shellsCache) put(v []Shell) {
	c.mu.Lock()
	c.value = v
	c.ready = true
	c.expires = time.Now().Add(c.ttl)
	c.mu.Unlock()
}

func (c *shellsCache) invalidate() {
	c.mu.Lock()
	c.ready = false
	c.value = nil
	c.mu.Unlock()
}

// Shells orchestrates plain shell sessions. It reuses the tmux client and the
// streaming machinery that back coder sessions, but carries no provider state.
type Shells struct {
	cfg            config.Config
	tmux           *tmux.Client
	projects       *project.Repository
	mapper         terminal.ControlMapper
	streams        *terminal.Hub
	listCache      *shellsCache
	historyEnabled func() bool
}

// NewShells wires up Shells with its dependencies. historyEnabled reads the
// per-shell history setting on every call; a nil func means the feature is off.
func NewShells(cfg config.Config, t *tmux.Client, projects *project.Repository, historyEnabled func() bool) *Shells {
	if historyEnabled == nil {
		historyEnabled = func() bool { return false }
	}
	return &Shells{
		cfg:            cfg,
		tmux:           t,
		projects:       projects,
		mapper:         terminal.DefaultControlMapper(),
		streams:        terminal.NewHub(cfg),
		listCache:      &shellsCache{ttl: cfg.SnapshotCacheTTL},
		historyEnabled: historyEnabled,
	}
}

// List returns every live shell session, sorted by tmux's pane order. The result
// is cached for a short TTL; mutations invalidate it.
func (s *Shells) List() []Shell {
	if v, ok := s.listCache.get(); ok {
		return v
	}
	panes, _ := s.tmux.ListPanes()
	var out []Shell
	for _, p := range panes {
		name := strings.TrimSpace(p.ShellName)
		if name == "" {
			continue
		}
		out = append(out, Shell{
			Identifier:   p.Name,
			TmuxSession:  p.Name,
			PID:          p.PID,
			Name:         name,
			StartedAt:    p.StartTime(),
			CWD:          strings.TrimSpace(p.Workdir),
			TabPos:       p.TabPosition(),
			TabGroup:     strings.TrimSpace(p.TabGroup),
			TabGroupPos:  p.TabGroupPosition(),
			TabGroupName: strings.TrimSpace(p.TabGName),
		})
	}
	s.listCache.put(out)
	return out
}

// Invalidate drops the cached shell list so the next List re-scans tmux.
func (s *Shells) Invalidate() {
	s.listCache.invalidate()
}

// ResolveRunning validates the identifier and returns the matching shell.
func (s *Shells) ResolveRunning(rawID string) (Shell, error) {
	id, err := terminal.ValidateIdentifier(rawID)
	if err != nil {
		return Shell{}, err
	}
	for _, sh := range s.List() {
		if sh.Identifier == id {
			return sh, nil
		}
	}
	return Shell{}, fmt.Errorf(`%w with identifier "%s" was found.`, ErrNotRunning, id)
}

// Resolve reports whether a shell with the given identifier is live.
func (s *Shells) Resolve(rawID string) error {
	_, err := s.ResolveRunning(rawID)
	return err
}

// Start launches a new shell session in workdir, labelled name.
func (s *Shells) Start(workdir, name string) (string, error) {
	key, err := terminal.NewKey()
	if err != nil {
		return "", err
	}
	return s.start(workdir, name, key)
}

// StartWithKey launches a shell under a caller-provided session key. The
// terminal restore uses it to bring a shell back under its recorded id, so
// links, open tabs, and notification entries keep resolving.
func (s *Shells) StartWithKey(workdir, name, key string) (string, error) {
	id, err := terminal.ValidateIdentifier(key)
	if err != nil {
		return "", err
	}
	return s.start(workdir, name, id)
}

func (s *Shells) start(workdir, name, key string) (string, error) {
	dir := strings.TrimSpace(workdir)
	if dir == "" {
		return "", errors.New("Shell directory is required.")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("Shell directory does not exist: %s", dir)
	}
	label := sanitizeShellName(name)
	if label == "" {
		label = "shell"
	}
	for _, p := range s.listPanesBestEffort() {
		if p.Name == key {
			return "", fmt.Errorf(`Shell "%s" already exists.`, key)
		}
	}
	env, err := s.shellEnv(key)
	if err != nil {
		return "", err
	}
	if err := s.tmux.NewSession(key, dir, "", env); err != nil {
		return "", err
	}
	if err := s.tmux.SetOption(key, shellNameOption, label); err != nil {
		return "", err
	}
	if err := s.tmux.SetOption(key, shellDirOption, dir); err != nil {
		return "", err
	}
	if err := s.tmux.SetHistoryLimit(key, s.cfg.TerminalHistoryLimit); err != nil {
		return "", err
	}
	s.Invalidate()
	return key, nil
}

// shellEnv builds the environment injected into a new shell. Every shell gets
// the OSC 133 prompt marks; with per-shell history on, the shell also points
// HISTFILE at its own id-keyed file and, on every prompt, flushes it with
// `history -a` and re-caps it by re-assigning HISTFILESIZE.
//
// `history -a` appends only the unwritten lines and tracks its own offset, so
// it never duplicates, not even against the exit-time save a login shell does
// with `histappend` on, and a tmux kill (SIGHUP, no clean exit) never drops
// the history. `-a` alone never truncates though, so a shell that is never
// restarted would grow the file without bound. Re-assigning HISTFILESIZE to its
// own value truncates the file to that many lines right then, so the cap holds
// mid-session too, not only on the startup assignment the login rc does. The
// value is the effective HISTFILESIZE (the rc's, bash's default, or one the
// user set live), so this respects the user's own history size.
//
// Keyed by id, the file survives a restore, which recreates the shell under the
// same id. An rc that overwrites HISTFILE or PROMPT_COMMAND silently turns this
// off, same caveat as the marks.
func (s *Shells) shellEnv(key string) (map[string]string, error) {
	env := shellMarkEnv()
	if !s.historyEnabled() {
		return env, nil
	}
	dir := s.historyDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	env["HISTFILE"] = filepath.Join(dir, key)
	env["PROMPT_COMMAND"] = "history -a; HISTFILESIZE=$HISTFILESIZE; " + env["PROMPT_COMMAND"]
	return env, nil
}

// historyDir is the directory holding the per-shell history files.
func (s *Shells) historyDir() string {
	return filepath.Join(s.cfg.StateDir, "shell-history")
}

// ReapHistory deletes per-shell history files that no live shell owns. Files
// are keyed by shell id and ids are never reused, so a file without a live
// shell is dead weight: a shell closed outside the cockpit, or a reboot without
// restore. Restore recreates shells under their old id before the startup reap
// runs, so a restored shell's file is kept. Runs regardless of the setting, so
// files left over from a former on-phase clean up too. Best effort, a missing
// directory is a no-op.
func (s *Shells) ReapHistory() {
	dir := s.historyDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	live := map[string]bool{}
	for _, sh := range s.List() {
		live[sh.Identifier] = true
	}
	for _, e := range entries {
		if e.IsDir() || live[e.Name()] {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}

// RunHistoryReaper reaps orphan history files every interval. Never returns,
// run it on a goroutine.
func (s *Shells) RunHistoryReaper(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		s.ReapHistory()
	}
}

// Rename changes a shell's display name.
func (s *Shells) Rename(rawID, rawName string) (Shell, error) {
	sh, err := s.ResolveRunning(rawID)
	if err != nil {
		return Shell{}, err
	}
	name := sanitizeShellName(rawName)
	if name == "" {
		return Shell{}, errors.New("Shell name is required.")
	}
	if err := s.tmux.SetOption(sh.TmuxSession, shellNameOption, name); err != nil {
		return Shell{}, err
	}
	sh.Name = name
	s.Invalidate()
	return sh, nil
}

// Delete kills the shell's tmux session and closes its control client.
func (s *Shells) Delete(rawID string) (string, error) {
	sh, err := s.ResolveRunning(rawID)
	if err != nil {
		return "", err
	}
	if err := s.tmux.Kill(sh.TmuxSession); err != nil {
		return "", err
	}
	s.streams.Clear(sh.TmuxSession)
	_ = os.Remove(filepath.Join(s.historyDir(), sh.Identifier))
	s.Invalidate()
	return sh.Name, nil
}

// shellScrollActions maps shell scroll control IDs to streamHub scroll actions.
// These scroll the tmux history instead of being sent to the program.
var shellScrollActions = map[string]string{
	"scroll-up":        "up",
	"scroll-down":      "down",
	"scroll-line-up":   "line-up",
	"scroll-line-down": "line-down",
	"scroll-top":       "top",
	"scroll-bottom":    "bottom",
}

// Send dispatches a batch of user inputs to a shell, in order. Scroll controls
// drive the tmux history view; everything else is forwarded to the program.
func (s *Shells) Send(rawID string, items []terminal.Input) error {
	target, sink, err := s.resolveInput(rawID)
	if err != nil {
		return err
	}
	for _, item := range items {
		if action, ok := shellScrollActions[strings.TrimSpace(item.Control)]; ok {
			s.streams.Scroll(target, action)
			continue
		}
		s.streams.ResumeLive(target)
		if err := terminal.SendInput(sink, s.mapper, target, item); err != nil {
			return err
		}
	}
	return nil
}

// resolveInput mirrors the coder manager input routing: while a browser stream is attached
// (the normal case when typing) keystrokes go over its persistent control
// connection, so the per-key path forks nothing and skips the pane listing.
// With no stream attached it falls back to the forking CLI.
func (s *Shells) resolveInput(rawID string) (string, terminal.Target, error) {
	id, err := terminal.ValidateIdentifier(rawID)
	if err != nil {
		return "", nil, err
	}
	if ctl := s.streams.Control(id); ctl != nil {
		return id, terminal.ControlInput{Ctl: ctl, CLI: s.tmux, Gone: ErrNotRunning}, nil
	}
	sh, err := s.ResolveRunning(id)
	if err != nil {
		return "", nil, err
	}
	return sh.TmuxSession, s.tmux, nil
}

// Resize sets the shell's rendered terminal size.
func (s *Shells) Resize(rawID, rawCols, rawRows string) error {
	sh, err := s.ResolveRunning(rawID)
	if err != nil {
		return err
	}
	cols, rows, err := terminal.ValidateDimensions(s.cfg, rawCols, rawRows)
	if err != nil {
		return err
	}
	return s.streams.Resize(sh.TmuxSession, cols, rows)
}

// AttachStream opens the control client and returns the initial snapshot.
func (s *Shells) AttachStream(rawID, rawCols, rawRows string) (terminal.Attachment, error) {
	sh, err := s.ResolveRunning(rawID)
	if err != nil {
		return terminal.Attachment{}, err
	}
	return s.streams.Attach(sh.TmuxSession, rawCols, rawRows)
}

// RefreshStream returns a new snapshot when another browser reset this stream.
func (s *Shells) RefreshStream(name string, generation int64) (terminal.Attachment, bool) {
	return s.streams.Refresh(name, generation)
}

// DetachStream releases one browser stream and closes the control client after
// the last one.
func (s *Shells) DetachStream(name string) { s.streams.Detach(name) }

// StreamDelta returns buffered output after offset.
func (s *Shells) StreamDelta(name string, offset int64) ([]byte, int64, bool) {
	return s.streams.Delta(name, offset)
}

// StreamUpdated returns a channel closed on the next output or exit.
func (s *Shells) StreamUpdated(name string) (<-chan struct{}, bool) {
	return s.streams.Updated(name)
}

// StreamExited reports whether the underlying control client has ended.
func (s *Shells) StreamExited(name string) bool { return s.streams.Exited(name) }

// Resnapshot recaptures the screen for a stream that fell out of the ring.
func (s *Shells) Resnapshot(name string) (terminal.Attachment, bool) {
	return s.streams.Resnapshot(name)
}

func (s *Shells) listPanesBestEffort() []tmux.Pane {
	panes, _ := s.tmux.ListPanes()
	return panes
}

// sanitizeShellName trims a user-supplied name and drops control characters
// (notably tab/newline, which would corrupt the tab-separated pane listing).
func sanitizeShellName(raw string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, raw)
	cleaned = strings.TrimSpace(cleaned)
	if len(cleaned) > maxShellNameLength {
		cleaned = strings.TrimSpace(cleaned[:maxShellNameLength])
	}
	return cleaned
}
