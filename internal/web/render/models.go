package render

// CoderModelDefaults is one coder's block on the Models tab of the assistant
// settings: the three purpose picks over its list, the effort beside each
// over its levels, and the note under them.
type CoderModelDefaults struct {
	ID            string
	Label         string
	Chat          AssistantModelPick
	Check         AssistantModelPick
	Trigger       AssistantModelPick
	ChatEffort    AssistantModelPick
	CheckEffort   AssistantModelPick
	TriggerEffort AssistantModelPick
	Note          string
	Warning       string
}

// SettingsAssistantModelsData feeds the Models tab of the assistant settings.
type SettingsAssistantModelsData struct {
	Page
	SettingsNav SettingsNav
	Section     string
	Coders      []CoderModelDefaults
}

// CoderModelsData feeds a coder's Models section: the start default over its
// list with the start effort beside it and the names its repository
// remembered, each with a Delete, plus the
// add row, whose field is bounded by MaxRunes.
type CoderModelsData struct {
	Page
	SettingsNav SettingsNav
	Base        string
	Start       AssistantModelPick
	StartEffort AssistantModelPick
	Note        string
	Warning     string
	Added       []string
	MaxRunes    int
}
