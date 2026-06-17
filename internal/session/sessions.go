package session

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/local/dev-cockpit/internal/config"
	"github.com/local/dev-cockpit/internal/keys"
	"github.com/local/dev-cockpit/internal/project"
	"github.com/local/dev-cockpit/internal/provider"
	"github.com/local/dev-cockpit/internal/term"
)

// ErrNoActiveSession marks lookups for identifiers without a live coder session.
var ErrNoActiveSession = errors.New("No active session")

// Sessions orchestrates the session lifecycle.
type Sessions struct {
	cfg       config.Config
	term      *term.Client
	provider  provider.Provider
	projects  *project.Repository
	snapshots *snapshotCache
}

// NewSessions wires up Sessions with its dependencies.
func NewSessions(
	cfg config.Config,
	t *term.Client,
	p provider.Provider,
	projects *project.Repository,
) *Sessions {
	return &Sessions{
		cfg:       cfg,
		term:      t,
		provider:  p,
		projects:  projects,
		snapshots: &snapshotCache{ttl: cfg.SnapshotCacheTTL},
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

func (s *Sessions) compute() Snapshot {
	panes, _ := s.term.Discover()
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
	// Distinguish "no session" from "a session that is not a coder session".
	for _, p := range s.discoverBestEffort() {
		if p.Name == id {
			return Running{}, errors.New("Refusing to interact with a session that is not associated with a coder session.")
		}
	}
	return Running{}, fmt.Errorf(`%w with identifier "%s" was found.`, ErrNoActiveSession, id)
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
	for _, p := range s.discoverBestEffort() {
		if p.Name == sessionKey {
			return StartResult{}, fmt.Errorf(`Session "%s" already exists.`, sessionKey)
		}
	}
	shellCmd := s.provider.SessionRuntime().StartCommand(sessionKey, name, workdir, agentID, opts.RemoteControl, opts.AutomaticApproval)
	if err := s.term.Spawn(term.SpawnConfig{Key: sessionKey, Workdir: workdir, Command: shellCmd, Env: s.providerEnv()}); err != nil {
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
		return provider.Session{}, fmt.Errorf(`Session "%s" cannot be resumed: its identifier is not usable as a session name.`, stored.SessionID)
	}
	for _, p := range s.discoverBestEffort() {
		if p.Name == stored.SessionID {
			return provider.Session{}, fmt.Errorf(`Session "%s" already exists.`, stored.SessionID)
		}
	}
	cmd := s.provider.SessionRuntime().ResumeCommand(stored.SessionID, stored.CWD, stored.RemoteControl, true)
	if err := s.term.Spawn(term.SpawnConfig{Key: stored.SessionID, Workdir: stored.CWD, Command: cmd, Env: s.providerEnv()}); err != nil {
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

// Stop kills the running session agent.
func (s *Sessions) Stop(rawID string) (string, error) {
	r, err := s.ResolveRunning(rawID)
	if err != nil {
		return "", err
	}
	if err := s.term.Kill(r.SessionKey); err != nil {
		return "", err
	}
	s.Invalidate()
	return r.Name, nil
}

// OpenStream attaches a browser to a session and returns the live byte stream
// together with the terminal size the program was set to.
func (s *Sessions) OpenStream(rawID, rawCols, rawRows string) (*term.Stream, int, int, error) {
	r, err := s.ResolveRunning(rawID)
	if err != nil {
		return nil, 0, 0, err
	}
	cols, rows := defaultStreamCols, defaultStreamRows
	if strings.TrimSpace(rawCols) != "" || strings.TrimSpace(rawRows) != "" {
		c, rw, derr := validateDimensions(s.cfg, rawCols, rawRows)
		if derr != nil {
			return nil, 0, 0, derr
		}
		cols, rows = c, rw
	}
	stream, err := s.term.OpenStream(r.SessionKey, cols, rows)
	if err != nil {
		return nil, 0, 0, err
	}
	return stream, cols, rows, nil
}

// Input is one queued user action; exactly one field is non-empty.
type Input struct {
	Prompt  string
	Control string
	Text    string
	Paste   string
}

// Send dispatches a batch of user inputs to a session, in order. It resolves the
// target once and builds every frame before sending, so an unsupported control
// fails the whole batch instead of leaving a half-applied prefix.
func (s *Sessions) Send(rawID string, items []Input) error {
	r, err := s.ResolveRunning(rawID)
	if err != nil {
		return err
	}
	var frames []term.InFrame
	for _, item := range items {
		fs, err := s.framesFor(item)
		if err != nil {
			return err
		}
		frames = append(frames, fs...)
	}
	if len(frames) == 0 {
		return nil
	}
	return s.term.Send(r.SessionKey, frames)
}

func (s *Sessions) framesFor(item Input) ([]term.InFrame, error) {
	switch {
	case strings.TrimSpace(item.Control) != "":
		mapped, ok := s.provider.ControlMapper().Map(item.Control)
		if !ok {
			return nil, fmt.Errorf(`Unsupported control input "%s".`, item.Control)
		}
		return []term.InFrame{term.KeyFrame(mapped)}, nil
	case item.Text != "":
		var frames []term.InFrame
		for _, ev := range keys.Decode(item.Text) {
			if ev.Key != "" {
				frames = append(frames, term.KeyFrame(ev.Key))
				continue
			}
			frames = append(frames, term.TextFrame(ev.Text))
		}
		return frames, nil
	case item.Paste != "":
		paste := promptPayload(item.Paste)
		if paste == "" {
			return nil, errors.New("Input text is required.")
		}
		return []term.InFrame{term.PasteFrame(paste)}, nil
	case item.Prompt != "":
		prompt := promptPayload(item.Prompt)
		if prompt == "" {
			return nil, errors.New("Input text is required.")
		}
		return []term.InFrame{term.PasteFrame(prompt), term.KeyFrame("Enter")}, nil
	}
	return nil, errors.New("Input is required.")
}

// Resize sets the terminal size of a running session.
func (s *Sessions) Resize(rawID, rawCols, rawRows string) error {
	r, err := s.ResolveRunning(rawID)
	if err != nil {
		return err
	}
	cols, rows, err := validateDimensions(s.cfg, rawCols, rawRows)
	if err != nil {
		return err
	}
	return s.term.Resize(r.SessionKey, cols, rows)
}

// --- helpers ---

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
			if err := s.term.Rename(tempKey, r.SessionID); err == nil {
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

func promptPayload(raw string) string {
	return strings.ReplaceAll(strings.ReplaceAll(raw, "\r\n", "\n"), "\r", "\n")
}

// discoverBestEffort returns the current sessions, treating a listing failure as
// "none".
func (s *Sessions) discoverBestEffort() []term.Pane {
	panes, _ := s.term.Discover()
	return panes
}

func validateDimensions(cfg config.Config, rawCols, rawRows string) (int, int, error) {
	cols, err := strconv.Atoi(strings.TrimSpace(rawCols))
	if err != nil {
		return 0, 0, errors.New("Terminal size must be numeric.")
	}
	rows, err := strconv.Atoi(strings.TrimSpace(rawRows))
	if err != nil {
		return 0, 0, errors.New("Terminal size must be numeric.")
	}
	if cols < cfg.MinTerminalCols {
		return 0, 0, errors.New("Terminal size is too small.")
	}
	if rows < cfg.MinTerminalRows {
		rows = cfg.MinTerminalRows
	}
	if cols > cfg.MaxTerminalCols {
		cols = cfg.MaxTerminalCols
	}
	if rows > cfg.MaxTerminalRows {
		rows = cfg.MaxTerminalRows
	}
	return cols, rows, nil
}

const (
	defaultStreamCols = 80
	defaultStreamRows = 30
)
