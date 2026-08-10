package claude

import (
	"path/filepath"
	"time"

	"github.com/local/dev-cockpit/internal/coder"
	"github.com/local/dev-cockpit/internal/filesystem"
	"github.com/local/dev-cockpit/internal/terminal"
)

// endOfOptions terminates claude's own flag parsing, so the operand behind it is
// taken as text even when it starts with a dash. claude's prompt is a positional
// argument, and a positional that looks like an option is parsed as one: the
// paste "-dxdebug.idekey=PHPSTORM" arrives as the short flag -d with a filter,
// the prompt is gone, and a print run ends with "Input must be provided", which
// the turn reports as a coder that stopped before it finished. Values behind a
// flag like --session-id, --resume, --name or --agent are safe, claude takes the
// next argument whatever it looks like.
const endOfOptions = "--"

type Coder struct {
	tools  []string
	agents coder.AgentRepository
	// sessions is held as its own type, not as the interface: a session's
	// transcript is also what says whether its turn is over, see activity.go.
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

// New builds the claude coder. notifyInbox is the directory the injected
// Stop/Notification hooks drop their event files into; empty disables the
// hook injection.
func New(notifyInbox string) *Coder {
	home, err := filesystem.HomeDir()
	if err != nil {
		home = "/root"
	}
	stateRoot := filepath.Join(home, ".claude", "projects")
	c := &Coder{
		tools:        []string{"claude"},
		agents:       coder.NewStandardAgentRepository(filepath.Join(home, ".claude", "agents"), ".md"),
		sessions:     &sessionRepository{stateRoot: stateRoot},
		skills:       coder.NewStandardSkillRepository(filepath.Join(home, ".claude", "skills")),
		instructions: coder.NewFileGlobalInstructions(filepath.Join(home, ".claude", "CLAUDE.md")),
		runtime:      runtime{notifyInbox: notifyInbox},
		controls:     controlMapper{base: terminal.DefaultControlMapper()},
	}
	c.assistantProbe = coder.NewCapabilityProbe(c.probeAssistant, 10*time.Second)
	return c
}

func (p *Coder) ID() string                                   { return "claude" }
func (p *Coder) RequiredTools() []string                      { return p.tools }
func (p *Coder) AgentRepository() coder.AgentRepository       { return p.agents }
func (p *Coder) SessionRepository() coder.SessionRepository   { return p.sessions }
func (p *Coder) SkillRepository() coder.SkillRepository       { return p.skills }
func (p *Coder) GlobalInstructions() coder.GlobalInstructions { return p.instructions }
func (p *Coder) SessionRuntime() coder.SessionRuntime         { return p.runtime }
func (p *Coder) ControlMapper() terminal.ControlMapper        { return p.controls }
