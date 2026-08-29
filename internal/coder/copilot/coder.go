package copilot

import (
	"path/filepath"
	"time"

	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/terminal"
)

type Coder struct {
	tools        []string
	agents       coder.AgentRepository
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

func New() *Coder {
	home, err := filesystem.HomeDir()
	if err != nil {
		home = "/root"
	}
	stateRoot := filepath.Join(home, ".copilot", "session-state")
	c := &Coder{
		tools:        []string{"copilot"},
		agents:       coder.NewStandardAgentRepository(filepath.Join(home, ".copilot", "agents"), ".agent.md"),
		sessions:     &sessionRepository{stateRoot: stateRoot},
		skills:       coder.NewStandardSkillRepository(filepath.Join(home, ".copilot", "skills")),
		instructions: coder.NewFileGlobalInstructions(filepath.Join(home, ".copilot", "copilot-instructions.md")),
		runtime:      runtime{},
		controls:     terminal.DefaultControlMapper(),
	}
	c.assistantProbe = coder.NewCapabilityProbe(c.probeAssistant, 10*time.Second)
	return c
}

func (p *Coder) ID() string                                   { return "copilot" }
func (p *Coder) RequiredTools() []string                      { return p.tools }
func (p *Coder) AgentRepository() coder.AgentRepository       { return p.agents }
func (p *Coder) SessionRepository() coder.SessionRepository   { return p.sessions }
func (p *Coder) SkillRepository() coder.SkillRepository       { return p.skills }
func (p *Coder) GlobalInstructions() coder.GlobalInstructions { return p.instructions }
func (p *Coder) SessionRuntime() coder.SessionRuntime         { return p.runtime }
func (p *Coder) ControlMapper() terminal.ControlMapper        { return p.controls }

// ActivityProfile: the event log is a readable record, so it is watched.
// Interrupt keys stay off: copilot writes its log densely enough that an
// abort speaks for itself, and the bell closes finished turns besides. The
// cap is the backstop for a lost bell or log entry.
func (p *Coder) ActivityProfile() coder.ActivityProfile {
	return coder.ActivityProfile{
		WatchRecord:        true,
		InterruptKeys:      false,
		OpenTurnCap:        30 * time.Minute,
		MovementStartGrace: 20 * time.Second,
	}
}
