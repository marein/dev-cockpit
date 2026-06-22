package session

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/local/dev-cockpit/internal/config"
	"github.com/local/dev-cockpit/internal/project"
	"github.com/local/dev-cockpit/internal/provider"
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

// Shell is one live tmux session running a plain interactive login shell.
type Shell struct {
	Identifier  string
	TmuxSession string
	PID         string
	Name        string
	StartedAt   time.Time
	CWD         string
}

// Shells orchestrates plain shell sessions. It reuses the tmux client and the
// streaming machinery that back coder sessions, but carries no provider state.
type Shells struct {
	cfg      config.Config
	tmux     *tmux.Client
	projects *project.Repository
	mapper   provider.ControlMapper
	streams  *streamHub
}

// NewShells wires up Shells with its dependencies.
func NewShells(cfg config.Config, t *tmux.Client, projects *project.Repository) *Shells {
	return &Shells{
		cfg:      cfg,
		tmux:     t,
		projects: projects,
		mapper:   provider.DefaultControlMapper(),
		streams:  newStreamHub(cfg),
	}
}

// List returns every live shell session, sorted by tmux's pane order.
func (s *Shells) List() []Shell {
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
			StartedAt:   paneStartTime(p.StartedAt),
			CWD:         strings.TrimSpace(p.Workdir),
		})
	}
	return out
}

// ResolveRunning validates the identifier and returns the matching shell.
func (s *Shells) ResolveRunning(rawID string) (Shell, error) {
	id, err := ValidateIdentifier(rawID)
	if err != nil {
		return Shell{}, err
	}
	for _, sh := range s.List() {
		if sh.Identifier == id {
			return sh, nil
		}
	}
	return Shell{}, fmt.Errorf(`%w with identifier "%s" was found.`, ErrNoActiveSession, id)
}

// Resolve reports whether a shell with the given identifier is live.
func (s *Shells) Resolve(rawID string) error {
	_, err := s.ResolveRunning(rawID)
	return err
}

// Start launches a new shell session in workdir, labelled name.
func (s *Shells) Start(workdir, name string) (string, error) {
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
	key, err := newSessionKey()
	if err != nil {
		return "", err
	}
	for _, p := range s.listPanesBestEffort() {
		if p.Name == key {
			return "", fmt.Errorf(`Shell "%s" already exists.`, key)
		}
	}
	if err := s.tmux.NewSession(key, dir, "exec bash -il", nil); err != nil {
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
	s.streams.clear(sh.TmuxSession)
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
func (s *Shells) Send(rawID string, items []Input) error {
	target, sink, err := s.inputTarget(rawID)
	if err != nil {
		return err
	}
	for _, item := range items {
		if action, ok := shellScrollActions[strings.TrimSpace(item.Control)]; ok {
			s.streams.scroll(target, action)
			continue
		}
		s.streams.resumeLive(target)
		if err := sendInput(sink, s.mapper, target, item); err != nil {
			return err
		}
	}
	return nil
}

// inputTarget mirrors Sessions.inputTarget: while a browser stream is attached
// (the normal case when typing) keystrokes go over its persistent control
// connection, so the per-key path forks nothing and skips the pane listing.
// With no stream attached it falls back to the forking CLI.
func (s *Shells) inputTarget(rawID string) (string, inputTarget, error) {
	id, err := ValidateIdentifier(rawID)
	if err != nil {
		return "", nil, err
	}
	if ctl := s.streams.control(id); ctl != nil {
		return id, controlInput{ctl: ctl, cli: s.tmux}, nil
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
	cols, rows, err := validateDimensions(s.cfg, rawCols, rawRows)
	if err != nil {
		return err
	}
	return s.streams.resize(sh.TmuxSession, cols, rows)
}

// AttachStream opens the control client and returns the initial snapshot.
func (s *Shells) AttachStream(rawID, rawCols, rawRows string) (StreamAttachment, error) {
	sh, err := s.ResolveRunning(rawID)
	if err != nil {
		return StreamAttachment{}, err
	}
	return s.streams.attach(sh.TmuxSession, rawCols, rawRows)
}

// RefreshStream returns a new snapshot when another browser reset this stream.
func (s *Shells) RefreshStream(name string, generation int64) (StreamAttachment, bool) {
	return s.streams.refresh(name, generation)
}

// DetachStream releases one browser stream and closes the control client after
// the last one.
func (s *Shells) DetachStream(name string) { s.streams.detach(name) }

// StreamDelta returns buffered output after offset.
func (s *Shells) StreamDelta(name string, offset int64) ([]byte, int64, bool) {
	return s.streams.delta(name, offset)
}

// StreamUpdated returns a channel closed on the next output or exit.
func (s *Shells) StreamUpdated(name string) (<-chan struct{}, bool) {
	return s.streams.updated(name)
}

// StreamExited reports whether the underlying control client has ended.
func (s *Shells) StreamExited(name string) bool { return s.streams.exited(name) }

// Resnapshot recaptures the screen for a stream that fell out of the ring.
func (s *Shells) Resnapshot(name string) (StreamAttachment, bool) {
	return s.streams.resnapshot(name)
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
