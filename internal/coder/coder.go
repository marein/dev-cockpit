package coder

import (
	"github.com/local/dev-cockpit/internal/assistant"
	"github.com/local/dev-cockpit/internal/coderlogin"
	"github.com/local/dev-cockpit/internal/terminal"
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

// LoginCapable is the optional browser login capability: the coder's CLI can
// be logged in headless, with the URL or code it prints carried into a
// dialog. A coder without it keeps every other surface and logs in on its own
// terminal.
type LoginCapable interface {
	WebLogin() coderlogin.Login
}

// WebLoginFor returns the coder's web login, or nil.
func WebLoginFor(c Coder) coderlogin.Login {
	capable, ok := c.(LoginCapable)
	if !ok {
		return nil
	}
	return capable.WebLogin()
}
