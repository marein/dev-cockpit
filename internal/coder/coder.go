package coder

import "github.com/local/dev-cockpit/internal/terminal"

// Coder is a dependency bag for coder-specific collaborators.
type Coder interface {
	ID() string
	RequiredTools() []string
	AgentRepository() AgentRepository
	SessionRepository() SessionRepository
	SkillRepository() SkillRepository
	GlobalInstructions() GlobalInstructions
	SessionRuntime() SessionRuntime
	ControlMapper() terminal.ControlMapper
}
