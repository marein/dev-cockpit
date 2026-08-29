package opencode

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/terminal"
)

// opencode (opencode.ai, verified against CLI 1.18.23) keeps its state in two
// places, and both honor the XDG base directories: configuration, agents,
// skills and the global AGENTS.md live under the config directory
// (~/.config/opencode), while every session, message and part lives as a row
// in one global SQLite database under the data directory
// (~/.local/share/opencode/opencode.db). The database is opencode's own:
// every write stays with the installed CLI (`opencode session delete`), and
// the readings open the file directly but strictly read only, because a CLI
// read boots opencode's JavaScript runtime every time, see db.go.

type Coder struct {
	tools  []string
	agents coder.AgentRepository
	// sessions is held as its own type, not as the interface: the session
	// rows are also what says whether a turn is over, see activity.go.
	sessions     *sessionRepository
	skills       coder.SkillRepository
	instructions coder.GlobalInstructions
	runtime      coder.SessionRuntime
	controls     terminal.ControlMapper
	// runner is probed on first use, see assistant.go. A CLI without the flags
	// a turn needs loses the conversations only, never its terminal.
	assistantProbe *coder.CapabilityProbe
	runner         *runner
}

// New builds the opencode coder. notifyInbox is the directory the injected
// notification plugin drops its event files into, claude's constructor
// shape; empty disables the injection.
func New(notifyInbox string) *Coder {
	config := configDir()
	data := dataDir()
	sessions := &sessionRepository{
		dataRoot: data,
		query:    databaseQuery(filepath.Join(data, dbFileName)),
		remove:   removeSession,
	}
	c := &Coder{
		tools:        []string{"opencode"},
		agents:       coder.NewStandardAgentRepository(filepath.Join(config, "agent"), ".md"),
		sessions:     sessions,
		skills:       coder.NewStandardSkillRepository(filepath.Join(config, "skills")),
		instructions: coder.NewFileGlobalInstructions(filepath.Join(config, "AGENTS.md")),
		runtime: runtime{
			sessions: sessions, create: createSession,
			notifyInbox: notifyInbox, ensurePlugin: ensureNotifyPlugin,
			ensureConfig: ensureSessionConfig,
		},
		controls: terminal.DefaultControlMapper(),
	}
	c.assistantProbe = coder.NewCapabilityProbe(c.probeAssistant, 10*time.Second)
	return c
}

func (p *Coder) ID() string                                   { return "opencode" }
func (p *Coder) RequiredTools() []string                      { return p.tools }
func (p *Coder) AgentRepository() coder.AgentRepository       { return p.agents }
func (p *Coder) SessionRepository() coder.SessionRepository   { return p.sessions }
func (p *Coder) SkillRepository() coder.SkillRepository       { return p.skills }
func (p *Coder) GlobalInstructions() coder.GlobalInstructions { return p.instructions }
func (p *Coder) SessionRuntime() coder.SessionRuntime         { return p.runtime }
func (p *Coder) ControlMapper() terminal.ControlMapper        { return p.controls }

// ActivityProfile: the record lives behind opencode's CLI, so there is
// nothing to watch; the injected plugin reports the turns instead (busy and
// idle events through the inbox). Interrupt keys stay off deliberately: an
// Escape here does not reliably mean abort, and with no readable record
// there is nothing to withdraw a wrong hint. The cap is the backstop for a
// lost plugin event.
func (p *Coder) ActivityProfile() coder.ActivityProfile {
	return coder.ActivityProfile{
		WatchRecord:        false,
		InterruptKeys:      false,
		OpenTurnCap:        30 * time.Minute,
		MovementStartGrace: 20 * time.Second,
	}
}

// configDir resolves opencode's configuration directory the way opencode
// does: $XDG_CONFIG_HOME/opencode, or ~/.config/opencode without the
// variable.
func configDir() string {
	if dir := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); dir != "" {
		return filepath.Join(dir, "opencode")
	}
	return filepath.Join(homeDir(), ".config", "opencode")
}

// dataDir resolves opencode's data directory, which is where the database
// lives: $XDG_DATA_HOME/opencode, or ~/.local/share/opencode without the
// variable.
func dataDir() string {
	if dir := strings.TrimSpace(os.Getenv("XDG_DATA_HOME")); dir != "" {
		return filepath.Join(dir, "opencode")
	}
	return filepath.Join(homeDir(), ".local", "share", "opencode")
}

func homeDir() string {
	home, err := filesystem.HomeDir()
	if err != nil {
		return "/root"
	}
	return home
}
