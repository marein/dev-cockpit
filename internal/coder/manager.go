package coder

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/terminal"
	"github.com/marein/dev-cockpit/internal/tmux"
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
	// hidden reports provider sessions that belong to another surface and
	// must not appear as coder sessions. Chats set it: a chat drives a real
	// provider session, and without the filter every chat would also show up
	// as a ghost resumable coder in the lists built from this manager.
	hidden func(sessionID string) bool
	// screenGap is the distance between the two reads the activity fallback
	// takes of a terminal, see activity.go. A field, so a test does not have to
	// wait it out.
	screenGap time.Duration
	// now and sleep are the prompt gate's clock, fields so a test does not
	// have to wait the gate out.
	now   func() time.Time
	sleep func(time.Duration)
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
		screenGap: screenSettleGap,
		now:       time.Now,
		sleep:     time.Sleep,
	}
}

// SetHidden installs the session visibility filter. It must be set before the
// first snapshot, and the predicate must never call back into this manager.
func (s *Manager) SetHidden(hidden func(sessionID string) bool) {
	s.hidden = hidden
	s.Invalidate()
}

// visibleSessions is the stored session list with the hidden ones removed.
// Every list the UI builds goes through it; ResumeReserved is the single,
// explicit way past it.
func (s *Manager) visibleSessions() []Session {
	all := s.coder.SessionRepository().List()
	if s.hidden == nil {
		return all
	}
	out := make([]Session, 0, len(all))
	for _, session := range all {
		if s.hidden(session.SessionID) {
			continue
		}
		out = append(out, session)
	}
	return out
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
	resumable := s.visibleSessions()
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
	for _, r := range s.visibleSessions() {
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
	shellCmd := s.coder.SessionRuntime().StartCommand(SessionStart{
		SessionID:         sessionKey,
		Name:              name,
		Workdir:           workdir,
		AgentID:           agentID,
		AutomaticApproval: opts.AutomaticApproval,
		Task:              strings.TrimSpace(opts.Task),
	})
	s.trustWorkdir(workdir)
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
	return s.resume(stored, false)
}

// ResumeReserved brings a session back that the visibility filter hides, the
// one deliberate way past it. A chat drives a real provider session and keeps
// it hidden from every coder surface; when the chat is handed over, this
// starts the terminal on that exact conversation instead of a fresh one.
//
// It goes through the same body as Resume, so pane tagging, environment and
// snapshot invalidation cannot drift apart. It cleans up after itself: a
// failure between the tmux start and the tagging kills the half-built session,
// otherwise the caller would be left with a pane it does not know about.
func (s *Manager) ResumeReserved(rawSessionID, rawWorkdir, title string) (Session, error) {
	id, err := terminal.ValidateIdentifier(rawSessionID)
	if err != nil {
		return Session{}, err
	}
	var stored Session
	found := false
	// Deliberately unfiltered: this is the caller that owns the reservation.
	for _, r := range s.coder.SessionRepository().List() {
		if r.SessionID == id {
			stored = r
			found = true
			break
		}
	}
	if !found {
		return Session{}, fmt.Errorf(`No stored conversation "%s" was found.`, id)
	}
	if workdir := strings.TrimSpace(rawWorkdir); workdir != "" && NormalizeCWD(workdir) != NormalizeCWD(stored.CWD) {
		return Session{}, errors.New("That conversation belongs to a different project.")
	}
	if strings.TrimSpace(stored.Name) == "" {
		stored.Name = strings.TrimSpace(title)
	}
	return s.resume(stored, true)
}

func (s *Manager) resume(stored Session, cleanupOnFailure bool) (Session, error) {
	if _, err := terminal.ValidateIdentifier(stored.SessionID); err != nil {
		return Session{}, fmt.Errorf(`Coder "%s" cannot be resumed: its identifier is not usable as a tmux session name.`, stored.SessionID)
	}
	for _, p := range s.listPanesBestEffort() {
		if p.Name == stored.SessionID {
			return Session{}, fmt.Errorf(`Coder "%s" already exists.`, stored.SessionID)
		}
	}
	cmd := s.coder.SessionRuntime().ResumeCommand(stored.SessionID, stored.CWD, true)
	s.trustWorkdir(stored.CWD)
	if err := s.tmux.NewSession(stored.SessionID, stored.CWD, cmd, s.coder.SessionRuntime().Env()); err != nil {
		return Session{}, err
	}
	fail := func(err error) (Session, error) {
		if cleanupOnFailure {
			if killErr := s.tmux.Kill(stored.SessionID); killErr != nil {
				log.Printf("coder: cleanup of a half-started session %s failed: %v", stored.SessionID, killErr)
			}
			s.Invalidate()
		}
		return Session{}, err
	}
	if err := s.configureTerminal(stored.SessionID); err != nil {
		return fail(err)
	}
	if err := s.tagCoderPane(stored.SessionID, stored.Name, stored.CWD); err != nil {
		return fail(err)
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

// StreamModes reads the pane's current terminal modes, so a stream can tell the
// browser when a program switched into or out of its full screen UI.
func (s *Manager) StreamModes(name string) (tmux.PaneModes, bool) {
	return s.streams.Modes(name)
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
// the target once and stops at the first failing item. A prompt into a freshly
// started session waits at the gate first, see gatePrompt; keys and raw input
// stay immediate, a dialog answer must not lag behind the dialog.
func (s *Manager) Send(rawID string, items []terminal.Input) error {
	target, sink, err := s.resolveInput(rawID)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.Prompt != "" {
			s.gatePrompt(sink, target, rawID)
		}
		if err := terminal.SendInput(sink, s.coder.ControlMapper(), target, item); err != nil {
			return err
		}
	}
	return nil
}

// The measured loss window after a session start (send-timing-report.md, 2026-07-27):
// a prompt is a paste plus a separately sent Enter, the paste always lands in
// the coder's input box, but the TUI binds its input handler some time after it
// comes up, and until then the Enter is dropped. The prompt then sits fully
// typed but unsubmitted, indefinitely. tmux 3.4 exposes no signal for the
// handler being bound: the TUI on the alternate screen is necessary but not
// sufficient, a send at the flip instant still lost the Enter, so a settle
// margin follows the first sighting. At 1s after start everything arrived in
// the measurement; the window and the margin are generous because the boundary
// moves with host load.
const (
	// promptGateWindow is how young a session has to be for the gate to run at
	// all. Prompts into anything older cost nothing.
	promptGateWindow = 5 * time.Second
	// promptGateSettle is the margin between the TUI first being seen on the
	// alternate screen and the send.
	promptGateSettle = time.Second
	promptGatePoll   = 50 * time.Millisecond
	// promptGateMax bounds the whole wait. A pane that never shows the coder's
	// TUI (the CLI crashed at startup) is sent to anyway, exactly as before the
	// gate existed. It stays under the 10s the CLI gives the whole input
	// request (internal/cli/act.go), so even that case answers instead of
	// timing out.
	promptGateMax = 8 * time.Second
)

// gatePrompt holds a prompt back while its session is too young to submit it.
// Best effort on purpose: a session this cannot resolve, or a transport that
// cannot report the pane's foreground, is sent to exactly as before.
func (s *Manager) gatePrompt(sink terminal.Target, target, rawID string) {
	fr, ok := sink.(terminal.ForegroundReporter)
	if !ok {
		return
	}
	r, err := s.ResolveRunning(rawID)
	if err != nil || r.StartedAt.IsZero() {
		return
	}
	awaitPromptReady(r.StartedAt, s.coder.ID(), func() (tmux.PaneForeground, bool) {
		fg, err := fr.PaneForeground(target)
		return fg, err == nil
	}, s.now, s.sleep)
}

// awaitPromptReady is the gate itself, its clock injected so a test does not
// wait it out. Ready is the coder's TUI running on the alternate screen plus
// the settle margin behind it.
func awaitPromptReady(started time.Time, coderCmd string, foreground func() (tmux.PaneForeground, bool), now func() time.Time, sleep func(time.Duration)) {
	if now().Sub(started) >= promptGateWindow {
		return
	}
	deadline := now().Add(promptGateMax)
	for {
		if fg, ok := foreground(); ok && fg.AltScreen && fg.Command == coderCmd {
			sleep(promptGateSettle)
			return
		}
		if !now().Before(deadline) {
			return
		}
		sleep(promptGatePoll)
	}
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

// trustWorkdir marks the directory a session is about to start in as trusted
// in the coder CLI's own configuration, before the session exists. A CLI that
// asks about an unknown folder asks before it reads the task out of its argv,
// so the coder would sit on a dialog while the caller was told it is working.
//
// Best effort on purpose: a coder that comes up on the dialog is still a coder,
// a project that cannot be opened because its CLI's config is unreadable is
// not. The reason is logged, the session starts either way.
func (s *Manager) trustWorkdir(workdir string) {
	truster, ok := s.coder.SessionRuntime().(WorkdirTruster)
	if !ok {
		return
	}
	if err := truster.TrustWorkdir(workdir); err != nil {
		log.Printf("coder %s: %s was not marked as trusted, the session may come up on the trust dialog: %v",
			s.coder.ID(), workdir, err)
	}
}

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
	// A CLI that cannot carry the cockpit's name into its session record
	// (coder.SessionNaming answering false) never produces a name match, so
	// its fresh session is the one that appeared in the working directory
	// after the start. The other guards stay: it must be new, it must be in
	// this project, and the window is short.
	named := true
	if naming, ok := s.coder.SessionRuntime().(SessionNaming); ok {
		named = naming.NamesSessions()
	}
	beforeIDs := map[string]bool{}
	for _, r := range before {
		beforeIDs[r.SessionID] = true
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		for _, r := range s.coder.SessionRepository().List() {
			if beforeIDs[r.SessionID] || r.CWD != workdir {
				continue
			}
			if named && strings.TrimSpace(r.Name) != displayName {
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
