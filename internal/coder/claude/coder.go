package claude

import (
	"path/filepath"

	"github.com/local/dev-cockpit/internal/coder"
	"github.com/local/dev-cockpit/internal/filesystem"
	"github.com/local/dev-cockpit/internal/terminal"
)

type Coder struct {
	tools        []string
	agents       coder.AgentRepository
	sessions     coder.SessionRepository
	skills       coder.SkillRepository
	instructions coder.GlobalInstructions
	runtime      coder.SessionRuntime
	controls     terminal.ControlMapper
}

func New() *Coder {
	home, err := filesystem.HomeDir()
	if err != nil {
		home = "/root"
	}
	stateRoot := filepath.Join(home, ".claude", "projects")
	return &Coder{
		tools:        []string{"claude"},
		agents:       coder.NewStandardAgentRepository(filepath.Join(home, ".claude", "agents"), ".md"),
		sessions:     &sessionRepository{stateRoot: stateRoot},
		skills:       coder.NewStandardSkillRepository(filepath.Join(home, ".claude", "skills")),
		instructions: coder.NewFileGlobalInstructions(filepath.Join(home, ".claude", "CLAUDE.md")),
		runtime:      runtime{},
		controls:     controlMapper{base: terminal.DefaultControlMapper()},
	}
}

func (p *Coder) ID() string                                   { return "claude" }
func (p *Coder) RequiredTools() []string                      { return p.tools }
func (p *Coder) AgentRepository() coder.AgentRepository       { return p.agents }
func (p *Coder) SessionRepository() coder.SessionRepository   { return p.sessions }
func (p *Coder) SkillRepository() coder.SkillRepository       { return p.skills }
func (p *Coder) GlobalInstructions() coder.GlobalInstructions { return p.instructions }
func (p *Coder) SessionRuntime() coder.SessionRuntime         { return p.runtime }
func (p *Coder) ControlMapper() terminal.ControlMapper        { return p.controls }
