package coder

import (
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/terminal"
)

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

// AssistantCapable is the optional conversation capability. A coder implements
// it when its CLI can answer a prompt non-interactively and stream the answer;
// the method returns nil when the installed version cannot. Terminal behavior
// does not depend on it, a coder without it keeps every other surface.
//
// The dependency points this way on purpose: internal/assistant never imports
// internal/coder, so the two packages cannot form a cycle.
type AssistantCapable interface {
	AssistantRunner() assistant.Runner
}

// AssistantRunnerFor returns the coder's conversation runner, or nil.
func AssistantRunnerFor(c Coder) assistant.Runner {
	capable, ok := c.(AssistantCapable)
	if !ok {
		return nil
	}
	return capable.AssistantRunner()
}
