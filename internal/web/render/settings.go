package render

// JingleOption is one selectable notification jingle. The IDs must match the
// tune keys in the client's @dc/jingle module.
type JingleOption struct {
	ID    string
	Label string
}

// SettingsNotificationsData feeds the notifications settings page.
type SettingsNotificationsData struct {
	Page
	Jingles  []JingleOption
	Selected string
}
