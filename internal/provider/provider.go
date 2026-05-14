package provider

// Provider is a dependency bag for provider-specific collaborators.
type Provider interface {
	ID() string
	RequiredTools() []string
	AgentRepository() AgentRepository
	SessionRepository() SessionRepository
	SkillRepository() SkillRepository
	GlobalInstructions() GlobalInstructions
	SessionRuntime() SessionRuntime
	ControlMapper() ControlMapper
}
