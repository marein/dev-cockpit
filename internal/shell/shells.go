package shell

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/local/dev-cockpit/internal/config"
	"github.com/local/dev-cockpit/internal/project"
	"github.com/local/dev-cockpit/internal/terminal"
	"github.com/local/dev-cockpit/internal/tmux"
)

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
	Identifier  string
	TmuxSession string
	PID         string
	Name        string
	StartedAt   time.Time
	CWD         string
	TabPos      int // tab strip position from @dc_tab_pos, 0 when unset
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
	cfg       config.Config
	tmux      *tmux.Client
	projects  *project.Repository
	mapper    terminal.ControlMapper
	streams   *terminal.Hub
	listCache *shellsCache
}

// NewShells wires up Shells with its dependencies.
func NewShells(cfg config.Config, t *tmux.Client, projects *project.Repository) *Shells {
	return &Shells{
		cfg:       cfg,
		tmux:      t,
		projects:  projects,
		mapper:    terminal.DefaultControlMapper(),
		streams:   terminal.NewHub(cfg),
		listCache: &shellsCache{ttl: cfg.SnapshotCacheTTL},
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
			Identifier:  p.Name,
			TmuxSession: p.Name,
			PID:         p.PID,
			Name:        name,
			StartedAt:   p.StartTime(),
			CWD:         strings.TrimSpace(p.Workdir),
			TabPos:      p.TabPosition(),
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
	if err := s.tmux.NewSession(key, dir, "exec bash -il", shellMarkEnv()); err != nil {
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
