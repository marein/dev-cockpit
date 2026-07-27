package copilot

import (
	"path/filepath"
	"sync"

	"github.com/local/dev-cockpit/internal/coder"
	"github.com/local/dev-cockpit/internal/filesystem"
	"github.com/local/dev-cockpit/internal/terminal"
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
	assistantOnce sync.Once
	runner        *runner
}

func New() *Coder {
	home, err := filesystem.HomeDir()
	if err != nil {
		home = "/root"
	}
	stateRoot := filepath.Join(home, ".copilot", "session-state")
	return &Coder{
		tools:        []string{"copilot"},
		agents:       coder.NewStandardAgentRepository(filepath.Join(home, ".copilot", "agents"), ".agent.md"),
		sessions:     &sessionRepository{stateRoot: stateRoot},
		skills:       coder.NewStandardSkillRepository(filepath.Join(home, ".copilot", "skills")),
		instructions: coder.NewFileGlobalInstructions(filepath.Join(home, ".copilot", "copilot-instructions.md")),
		runtime:      runtime{},
		controls:     terminal.DefaultControlMapper(),
	}
}

func (p *Coder) ID() string                                   { return "copilot" }
func (p *Coder) RequiredTools() []string                      { return p.tools }
func (p *Coder) AgentRepository() coder.AgentRepository       { return p.agents }
func (p *Coder) SessionRepository() coder.SessionRepository   { return p.sessions }
func (p *Coder) SkillRepository() coder.SkillRepository       { return p.skills }
func (p *Coder) GlobalInstructions() coder.GlobalInstructions { return p.instructions }
func (p *Coder) SessionRuntime() coder.SessionRuntime         { return p.runtime }
func (p *Coder) ControlMapper() terminal.ControlMapper        { return p.controls }
