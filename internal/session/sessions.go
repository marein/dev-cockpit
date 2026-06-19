package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/local/dev-cockpit/internal/config"
	"github.com/local/dev-cockpit/internal/project"
	"github.com/local/dev-cockpit/internal/provider"
	"github.com/local/dev-cockpit/internal/tmux"
)

// ErrNoActiveSession marks lookups for identifiers without a live coder session.
var ErrNoActiveSession = errors.New("No active session")

// Sessions orchestrates the session lifecycle.
type Sessions struct {
	cfg       config.Config
	tmux      *tmux.Client
	provider  provider.Provider
	projects  *project.Repository
	snapshots *snapshotCache
	streams   *streamHub
}

// NewSessions wires up Sessions with its dependencies.
func NewSessions(
	cfg config.Config,
	t *tmux.Client,
	p provider.Provider,
	projects *project.Repository,
) *Sessions {
	return &Sessions{
		cfg:       cfg,
		tmux:      t,
		provider:  p,
		projects:  projects,
		snapshots: &snapshotCache{ttl: cfg.SnapshotCacheTTL},
		streams:   newStreamHub(cfg),
	}
}

// Snapshot returns the cached view of running/resumable sessions, recomputing
// it after the TTL or an Invalidate.
func (s *Sessions) Snapshot() Snapshot {
	if snap, ok := s.snapshots.get(); ok {
		return snap
	}
	snap := s.compute()
	s.snapshots.put(snap)
	return snap
}

// Invalidate flushes the snapshot cache.
func (s *Sessions) Invalidate() { s.snapshots.invalidate() }

// StopIdleStreams clears inherited tmux pipes left by a previous process.
func (s *Sessions) StopIdleStreams() error {
	s.Invalidate()
	var errs []error
	for _, r := range s.Snapshot().Running {
		if err := s.tmux.StopPipe(r.TmuxSession); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Sessions) compute() Snapshot {
	panes, _ := s.tmux.ListPanes()
	resumable := s.provider.SessionRepository().List()
	running, inactive := scanRunning(panes, resumable, s.provider)
	return Snapshot{Running: running, Inactive: inactive, Resumable: resumable}
}

// ResolveRunning validates the identifier and returns the matching Running entry.
func (s *Sessions) ResolveRunning(rawID string) (Running, error) {
	id, err := ValidateIdentifier(rawID)
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
			return Running{}, errors.New("Refusing to interact with a tmux session that is not associated with a coder session.")
		}
	}
	return Running{}, fmt.Errorf(`%w with identifier "%s" was found.`, ErrNoActiveSession, id)
}

// Resolve reports whether a coder session with the given identifier is live.
func (s *Sessions) Resolve(rawID string) error {
	_, err := s.ResolveRunning(rawID)
	return err
}

// ResolveResumable looks up a stored session by id.
func (s *Sessions) ResolveResumable(rawID string) (provider.Session, error) {
	id := strings.TrimSpace(rawID)
	if id == "" {
		return provider.Session{}, errors.New("Session identifier is required.")
	}
	for _, r := range s.provider.SessionRepository().List() {
		if r.SessionID == id {
			return r, nil
		}
	}
	return provider.Session{}, fmt.Errorf(`No inactive session "%s" was found.`, id)
}

// StartResult is returned by Start.
type StartResult struct {
	Identifier string
	Name       string
	Workdir    string
	AgentID    string
}

// Start creates a new coder session.
func (s *Sessions) Start(rawName, rawProject, rawAgent string, opts StartOptions) (StartResult, error) {
	name := strings.TrimSpace(rawName)
	if name == "" {
		return StartResult{}, errors.New("Session name is required.")
	}
	workdir, err := s.projects.ValidatePath(rawProject)
	if err != nil {
		return StartResult{}, err
	}
	agentID, err := s.provider.AgentRepository().ValidateSelected(rawAgent)
	if err != nil {
		return StartResult{}, err
	}
	var before []provider.Session
	if !s.provider.SessionRuntime().UsesProvidedSessionID() {
		before = s.provider.SessionRepository().List()
	}
	sessionKey, err := newSessionKey()
	if err != nil {
		return StartResult{}, err
	}
	for _, p := range s.listPanesBestEffort() {
		if p.Name == sessionKey {
			return StartResult{}, fmt.Errorf(`Session "%s" already exists.`, sessionKey)
		}
	}
	shellCmd := s.provider.SessionRuntime().StartCommand(sessionKey, name, workdir, agentID, opts.RemoteControl, opts.AutomaticApproval)
	if err := s.tmux.NewSession(sessionKey, workdir, shellCmd, s.providerEnv()); err != nil {
		return StartResult{}, err
	}
	if err := s.configureTerminal(sessionKey); err != nil {
		return StartResult{}, err
	}
	identifier := sessionKey
	if !s.provider.SessionRuntime().UsesProvidedSessionID() {
		identifier = s.promoteSessionKey(sessionKey, before, workdir, name)
	}
	s.Invalidate()
	return StartResult{Identifier: identifier, Name: name, Workdir: workdir, AgentID: agentID}, nil
}

// Resume brings a stored session back to life.
func (s *Sessions) Resume(rawID string) (provider.Session, error) {
	stored, err := s.ResolveResumable(rawID)
	if err != nil {
		return provider.Session{}, err
	}
	if _, err := ValidateIdentifier(stored.SessionID); err != nil {
		return provider.Session{}, fmt.Errorf(`Session "%s" cannot be resumed: its identifier is not usable as a tmux session name.`, stored.SessionID)
	}
	for _, p := range s.listPanesBestEffort() {
		if p.Name == stored.SessionID {
			return provider.Session{}, fmt.Errorf(`Session "%s" already exists.`, stored.SessionID)
		}
	}
	cmd := s.provider.SessionRuntime().ResumeCommand(stored.SessionID, stored.CWD, stored.RemoteControl, true)
	if err := s.tmux.NewSession(stored.SessionID, stored.CWD, cmd, s.providerEnv()); err != nil {
		return provider.Session{}, err
	}
	if err := s.configureTerminal(stored.SessionID); err != nil {
		return provider.Session{}, err
	}
	s.Invalidate()
	return stored, nil
}

// DeleteResumable removes the stored session directory.
func (s *Sessions) DeleteResumable(rawID string) (provider.Session, error) {
	stored, err := s.ResolveResumable(rawID)
	if err != nil {
		return provider.Session{}, err
	}
	for _, r := range s.Snapshot().Running {
		if r.Identifier == stored.SessionID {
			return provider.Session{}, fmt.Errorf(`Cannot delete inactive session "%s" while it is active.`, stored.Name)
		}
	}
	if err := s.provider.SessionRepository().DeleteSession(stored.SessionID); err != nil {
		return provider.Session{}, err
	}
	s.Invalidate()
	return stored, nil
}

// Stop kills the running tmux session and closes its control client.
func (s *Sessions) Stop(rawID string) (string, error) {
	r, err := s.ResolveRunning(rawID)
	if err != nil {
		return "", err
	}
	if err := s.tmux.Kill(r.TmuxSession); err != nil {
		return "", err
	}
	s.streams.clear(r.TmuxSession)
	s.Invalidate()
	return r.Name, nil
}

// AttachStream opens the control client and returns the initial snapshot.
func (s *Sessions) AttachStream(rawID, rawCols, rawRows string) (StreamAttachment, error) {
	r, err := s.ResolveRunning(rawID)
	if err != nil {
		return StreamAttachment{}, err
	}
	return s.streams.attach(r.TmuxSession, rawCols, rawRows)
}

// RefreshStream returns a new snapshot when another browser reset this stream.
func (s *Sessions) RefreshStream(name string, generation int64) (StreamAttachment, bool) {
	return s.streams.refresh(name, generation)
}

// DetachStream releases one browser stream and closes the control client after
// the last one.
func (s *Sessions) DetachStream(name string) { s.streams.detach(name) }

// StreamDelta returns buffered output after offset. reset is true when the
// caller fell out of the ring and must re-snapshot.
func (s *Sessions) StreamDelta(name string, offset int64) ([]byte, int64, bool) {
	return s.streams.delta(name, offset)
}

// StreamUpdated returns a channel closed on the next output or exit, plus
// whether the stream is still live.
func (s *Sessions) StreamUpdated(name string) (<-chan struct{}, bool) {
	return s.streams.updated(name)
}

// StreamExited reports whether the underlying control client has ended.
func (s *Sessions) StreamExited(name string) bool {
	return s.streams.exited(name)
}

// Resnapshot recaptures the screen for a stream that fell out of the ring.
func (s *Sessions) Resnapshot(name string) (StreamAttachment, bool) {
	return s.streams.resnapshot(name)
}

// Input is one queued user action; exactly one field is non-empty.
type Input struct {
	Prompt  string
	Control string
	Text    string
	Paste   string
	Raw     string
}

// Send dispatches a batch of user inputs to a session, in order. It resolves
// the target once and stops at the first failing item.
func (s *Sessions) Send(rawID string, items []Input) error {
	r, err := s.ResolveRunning(rawID)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := sendInput(s.tmux, s.provider.ControlMapper(), r.TmuxSession, item); err != nil {
			return err
		}
	}
	return nil
}

// Resize sets the tmux window size.
func (s *Sessions) Resize(rawID, rawCols, rawRows string) error {
	r, err := s.ResolveRunning(rawID)
	if err != nil {
		return err
	}
	cols, rows, err := validateDimensions(s.cfg, rawCols, rawRows)
	if err != nil {
		return err
	}
	return s.streams.resize(r.TmuxSession, cols, rows)
}

// --- helpers ---

func (s *Sessions) configureTerminal(name string) error {
	// Enable tmux's mouse option so coder TUIs that probe it at startup (claude)
	// don't nag the user to "scroll with PgUp/PgDn" — the browser already
	// forwards real wheel input. Cosmetic and best-effort; the option is
	// otherwise inert in our control-mode setup (input goes via send-keys).
	_ = s.tmux.SetOption(name, "mouse", "on")
	return s.tmux.SetHistoryLimit(name, s.cfg.TerminalHistoryLimit)
}

func (s *Sessions) providerEnv() map[string]string {
	env := map[string]string{provider.ProviderEnvVar: s.provider.ID()}
	for k, v := range s.provider.SessionRuntime().Env() {
		env[k] = v
	}
	return env
}

func newSessionKey() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	id := hex.EncodeToString(raw[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", id[0:8], id[8:12], id[12:16], id[16:20], id[20:32]), nil
}

func (s *Sessions) promoteSessionKey(tempKey string, before []provider.Session, workdir, displayName string) string {
	beforeIDs := map[string]bool{}
	for _, r := range before {
		beforeIDs[r.SessionID] = true
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, r := range s.provider.SessionRepository().List() {
			if beforeIDs[r.SessionID] || r.CWD != workdir || strings.TrimSpace(r.Name) != displayName {
				continue
			}
			if _, err := ValidateIdentifier(r.SessionID); err != nil {
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
func (s *Sessions) listPanesBestEffort() []tmux.Pane {
	panes, _ := s.tmux.ListPanes()
	return panes
}
