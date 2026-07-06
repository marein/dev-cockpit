package coder

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/local/dev-cockpit/internal/config"
	"github.com/local/dev-cockpit/internal/project"
	"github.com/local/dev-cockpit/internal/terminal"
	"github.com/local/dev-cockpit/internal/tmux"
)

// ErrNotRunning marks lookups for identifiers without a live coder session.
var ErrNotRunning = errors.New("No active coder")

// Coder panes are marked with tmux user options, mirroring the shell options in
// shells.go, so one list-panes call attributes every pane without a process
// scan. The display name and directory are launch-time values; for panes with a
// store record the store stays the source of truth (it also carries renames).
const (
	coderOption     = "@dc_coder"
	coderNameOption = "@dc_coder_name"
	coderDirOption  = "@dc_coder_dir"
)

// Manager orchestrates the session lifecycle.
type Manager struct {
	cfg       config.Config
	tmux      *tmux.Client
	coder     Coder
	projects  *project.Repository
	snapshots *snapshotCache
	streams   *terminal.Hub
}

// NewManager wires up a coder Manager with its dependencies.
func NewManager(
	cfg config.Config,
	t *tmux.Client,
	p Coder,
	projects *project.Repository,
) *Manager {
	return &Manager{
		cfg:       cfg,
		tmux:      t,
		coder:     p,
		projects:  projects,
		snapshots: &snapshotCache{ttl: cfg.SnapshotCacheTTL},
		streams:   terminal.NewHub(cfg),
	}
}

// Snapshot returns the cached view of running/resumable sessions, recomputing
// it after the TTL or an Invalidate.
func (s *Manager) Snapshot() Snapshot {
	if snap, ok := s.snapshots.get(); ok {
		return snap
	}
	snap := s.compute()
	s.snapshots.put(snap)
	return snap
}

// Coder returns the coder definition this manager serves.
func (s *Manager) Coder() Coder { return s.coder }

// ID returns the coder id.
func (s *Manager) ID() string { return s.coder.ID() }

// Invalidate flushes the snapshot cache.
func (s *Manager) Invalidate() { s.snapshots.invalidate() }

// StopIdleStreams clears inherited tmux pipes left by a previous process.
func (s *Manager) StopIdleStreams() error {
	s.Invalidate()
	var errs []error
	for _, r := range s.Snapshot().Running {
		if err := s.tmux.StopPipe(r.TmuxSession); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Manager) compute() Snapshot {
	panes, _ := s.tmux.ListPanes()
	resumable := s.coder.SessionRepository().List()
	running, inactive := scanRunning(panes, resumable, s.coder)
	return Snapshot{Running: running, Inactive: inactive, Resumable: resumable}
}

// ResolveRunning validates the identifier and returns the matching Running entry.
func (s *Manager) ResolveRunning(rawID string) (Running, error) {
	id, err := terminal.ValidateIdentifier(rawID)
	if err != nil {
		return Running{}, err
	}
	snap := s.Snapshot()
	for _, r := range snap.Running {
		if r.Identifier == id {
			return r, nil
		}
	}
	// Distinguish "no tmux session" from "tmux session is not a coder session".
	for _, p := range s.listPanesBestEffort() {
		if p.Name == id {
			return Running{}, errors.New("Refusing to interact with a tmux session that is not associated with a coder.")
		}
	}
	return Running{}, fmt.Errorf(`%w with identifier "%s" was found.`, ErrNotRunning, id)
}

// Resolve reports whether a coder session with the given identifier is live.
func (s *Manager) Resolve(rawID string) error {
	_, err := s.ResolveRunning(rawID)
	return err
}

// ResolveResumable looks up a stored session by id.
func (s *Manager) ResolveResumable(rawID string) (Session, error) {
	id := strings.TrimSpace(rawID)
	if id == "" {
		return Session{}, errors.New("Coder identifier is required.")
	}
	for _, r := range s.coder.SessionRepository().List() {
		if r.SessionID == id {
			return r, nil
		}
	}
	return Session{}, fmt.Errorf(`No inactive coder "%s" was found.`, id)
}

// StartResult is returned by Start.
type StartResult struct {
	Identifier string
	Name       string
	Workdir    string
	AgentID    string
}

// Start creates a new coder session.
func (s *Manager) Start(rawName, rawProject, rawAgent string, opts StartOptions) (StartResult, error) {
	name := strings.TrimSpace(rawName)
	if name == "" {
		return StartResult{}, errors.New("Coder name is required.")
	}
	workdir, err := s.projects.ValidatePath(rawProject)
	if err != nil {
		return StartResult{}, err
	}
	agentID, err := s.coder.AgentRepository().ValidateSelected(rawAgent)
	if err != nil {
		return StartResult{}, err
	}
	var before []Session
	if !s.coder.SessionRuntime().UsesProvidedSessionID() {
		before = s.coder.SessionRepository().List()
	}
	sessionKey, err := terminal.NewKey()
	if err != nil {
		return StartResult{}, err
	}
	for _, p := range s.listPanesBestEffort() {
		if p.Name == sessionKey {
			return StartResult{}, fmt.Errorf(`Coder "%s" already exists.`, sessionKey)
		}
	}
	shellCmd := s.coder.SessionRuntime().StartCommand(sessionKey, name, workdir, agentID, opts.RemoteControl, opts.AutomaticApproval)
	if err := s.tmux.NewSession(sessionKey, workdir, shellCmd, s.coder.SessionRuntime().Env()); err != nil {
		return StartResult{}, err
	}
	if err := s.configureTerminal(sessionKey); err != nil {
		return StartResult{}, err
	}
	if err := s.tagCoderPane(sessionKey, name, workdir); err != nil {
		return StartResult{}, err
	}
	identifier := sessionKey
	if !s.coder.SessionRuntime().UsesProvidedSessionID() {
		identifier = s.promoteSessionKey(sessionKey, before, workdir, name)
	}
	s.Invalidate()
	return StartResult{Identifier: identifier, Name: name, Workdir: workdir, AgentID: agentID}, nil
}

// Resume brings a stored session back to life.
func (s *Manager) Resume(rawID string) (Session, error) {
	stored, err := s.ResolveResumable(rawID)
	if err != nil {
		return Session{}, err
	}
	if _, err := terminal.ValidateIdentifier(stored.SessionID); err != nil {
		return Session{}, fmt.Errorf(`Coder "%s" cannot be resumed: its identifier is not usable as a tmux session name.`, stored.SessionID)
	}
	for _, p := range s.listPanesBestEffort() {
		if p.Name == stored.SessionID {
			return Session{}, fmt.Errorf(`Coder "%s" already exists.`, stored.SessionID)
		}
	}
	cmd := s.coder.SessionRuntime().ResumeCommand(stored.SessionID, stored.CWD, stored.RemoteControl, true)
	if err := s.tmux.NewSession(stored.SessionID, stored.CWD, cmd, s.coder.SessionRuntime().Env()); err != nil {
		return Session{}, err
	}
	if err := s.configureTerminal(stored.SessionID); err != nil {
		return Session{}, err
	}
	if err := s.tagCoderPane(stored.SessionID, stored.Name, stored.CWD); err != nil {
		return Session{}, err
	}
	s.Invalidate()
	return stored, nil
}

// DeleteResumable removes the stored session directory.
func (s *Manager) DeleteResumable(rawID string) (Session, error) {
	stored, err := s.ResolveResumable(rawID)
	if err != nil {
		return Session{}, err
	}
	for _, r := range s.Snapshot().Running {
		if r.Identifier == stored.SessionID {
			return Session{}, fmt.Errorf(`Cannot delete inactive coder "%s" while it is active.`, stored.Name)
		}
	}
	if err := s.coder.SessionRepository().DeleteSession(stored.SessionID); err != nil {
		return Session{}, err
	}
	s.Invalidate()
	return stored, nil
}

// Stop kills the running tmux session and closes its control client.
func (s *Manager) Stop(rawID string) (string, error) {
	r, err := s.ResolveRunning(rawID)
	if err != nil {
		return "", err
	}
	if err := s.tmux.Kill(r.TmuxSession); err != nil {
		return "", err
	}
	s.streams.Clear(r.TmuxSession)
	s.Invalidate()
	return r.Name, nil
}

// AttachStream opens the control client and returns the initial snapshot.
func (s *Manager) AttachStream(rawID, rawCols, rawRows string) (terminal.Attachment, error) {
	r, err := s.ResolveRunning(rawID)
	if err != nil {
		return terminal.Attachment{}, err
	}
	return s.streams.Attach(r.TmuxSession, rawCols, rawRows)
}

// RefreshStream returns a new snapshot when another browser reset this stream.
func (s *Manager) RefreshStream(name string, generation int64) (terminal.Attachment, bool) {
	return s.streams.Refresh(name, generation)
}

// DetachStream releases one browser stream and closes the control client after
// the last one.
func (s *Manager) DetachStream(name string) { s.streams.Detach(name) }

// StreamDelta returns buffered output after offset. reset is true when the
// caller fell out of the ring and must re-snapshot.
func (s *Manager) StreamDelta(name string, offset int64) ([]byte, int64, bool) {
	return s.streams.Delta(name, offset)
}

// StreamUpdated returns a channel closed on the next output or exit, plus
// whether the stream is still live.
func (s *Manager) StreamUpdated(name string) (<-chan struct{}, bool) {
	return s.streams.Updated(name)
}

// StreamExited reports whether the underlying control client has ended.
func (s *Manager) StreamExited(name string) bool {
	return s.streams.Exited(name)
}

// Resnapshot recaptures the screen for a stream that fell out of the ring.
func (s *Manager) Resnapshot(name string) (terminal.Attachment, bool) {
	return s.streams.Resnapshot(name)
}

// Send dispatches a batch of user inputs to a session, in order. It resolves
// the target once and stops at the first failing item.
func (s *Manager) Send(rawID string, items []terminal.Input) error {
	target, sink, err := s.resolveInput(rawID)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := terminal.SendInput(sink, s.coder.ControlMapper(), target, item); err != nil {
			return err
		}
	}
	return nil
}

// resolveInput resolves the tmux target and the cheapest transport for input.
// While a browser stream is attached (the normal case when typing) keystrokes
// go over its persistent control connection, so the per-key path forks nothing
// and never runs the process-table snapshot. With no stream attached it falls
// back to the forking CLI, verifying liveness via the (cached) snapshot exactly
// as before.
func (s *Manager) resolveInput(rawID string) (string, terminal.Target, error) {
	id, err := terminal.ValidateIdentifier(rawID)
	if err != nil {
		return "", nil, err
	}
	if ctl := s.streams.Control(id); ctl != nil {
		return id, terminal.ControlInput{Ctl: ctl, CLI: s.tmux, Gone: ErrNotRunning}, nil
	}
	r, err := s.ResolveRunning(id)
	if err != nil {
		return "", nil, err
	}
	return r.TmuxSession, s.tmux, nil
}

// OwnsStream reports whether this service has a live browser stream for the
// identifier, letting callers route input without a process-table scan.
func (s *Manager) OwnsStream(rawID string) bool {
	id, err := terminal.ValidateIdentifier(rawID)
	if err != nil {
		return false
	}
	return s.streams.Control(id) != nil
}

// Resize sets the tmux window size.
func (s *Manager) Resize(rawID, rawCols, rawRows string) error {
	r, err := s.ResolveRunning(rawID)
	if err != nil {
		return err
	}
	cols, rows, err := terminal.ValidateDimensions(s.cfg, rawCols, rawRows)
	if err != nil {
		return err
	}
	return s.streams.Resize(r.TmuxSession, cols, rows)
}

// --- helpers ---

func (s *Manager) configureTerminal(name string) error {
	// Enable tmux's mouse option so coder TUIs that probe it at startup (claude)
	// don't nag the user to "scroll with PgUp/PgDn" — the browser already
	// forwards real wheel input. Cosmetic and best-effort; the option is
	// otherwise inert in our control-mode setup (input goes via send-keys).
	_ = s.tmux.SetOption(name, "mouse", "on")
	return s.tmux.SetHistoryLimit(name, s.cfg.TerminalHistoryLimit)
}

// tagCoderPane marks a freshly created tmux session as a pane of this coder so
// the snapshot can attribute it from list-panes alone.
func (s *Manager) tagCoderPane(name, displayName, workdir string) error {
	if err := s.tmux.SetOption(name, coderOption, s.coder.ID()); err != nil {
		return err
	}
	if err := s.tmux.SetOption(name, coderNameOption, displayName); err != nil {
		return err
	}
	return s.tmux.SetOption(name, coderDirOption, workdir)
}

func (s *Manager) promoteSessionKey(tempKey string, before []Session, workdir, displayName string) string {
	beforeIDs := map[string]bool{}
	for _, r := range before {
		beforeIDs[r.SessionID] = true
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, r := range s.coder.SessionRepository().List() {
			if beforeIDs[r.SessionID] || r.CWD != workdir || strings.TrimSpace(r.Name) != displayName {
				continue
			}
			if _, err := terminal.ValidateIdentifier(r.SessionID); err != nil {
				return tempKey
			}
			if err := s.tmux.Rename(tempKey, r.SessionID); err == nil {
				return r.SessionID
			}
			return tempKey
		}
		if time.Now().After(deadline) {
			return tempKey
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// listPanesBestEffort returns the current panes, treating a listing failure
// as "no panes".
func (s *Manager) listPanesBestEffort() []tmux.Pane {
	panes, _ := s.tmux.ListPanes()
	return panes
}
