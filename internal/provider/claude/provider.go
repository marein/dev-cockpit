package claude

import (
	"path/filepath"

	"github.com/local/dev-cockpit/internal/filesystem"
	"github.com/local/dev-cockpit/internal/provider"
)

type Provider struct {
	tools        []string
	agents       provider.AgentRepository
	sessions     provider.SessionRepository
	skills       provider.SkillRepository
	instructions provider.GlobalInstructions
	runtime      provider.SessionRuntime
	controls     provider.ControlMapper
}

func New() *Provider {
	home, err := filesystem.HomeDir()
	if err != nil {
		home = "/root"
	}
	stateRoot := filepath.Join(home, ".claude", "projects")
	return &Provider{
		tools:        []string{"claude"},
		agents:       provider.NewStandardAgentRepository(filepath.Join(home, ".claude", "agents"), ".md"),
		sessions:     &sessionRepository{stateRoot: stateRoot},
		skills:       provider.NewStandardSkillRepository(filepath.Join(home, ".claude", "skills")),
		instructions: provider.NewFileGlobalInstructions(filepath.Join(home, ".claude", "CLAUDE.md")),
		runtime:      runtime{},
		controls:     controlMapper{base: provider.DefaultControlMapper()},
	}
}

func (p *Provider) ID() string                                      { return "claude" }
func (p *Provider) RequiredTools() []string                         { return p.tools }
func (p *Provider) AgentRepository() provider.AgentRepository       { return p.agents }
func (p *Provider) SessionRepository() provider.SessionRepository   { return p.sessions }
func (p *Provider) SkillRepository() provider.SkillRepository       { return p.skills }
func (p *Provider) GlobalInstructions() provider.GlobalInstructions { return p.instructions }
func (p *Provider) SessionRuntime() provider.SessionRuntime         { return p.runtime }
func (p *Provider) ControlMapper() provider.ControlMapper           { return p.controls }
