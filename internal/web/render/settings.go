package render

// JingleOption is one selectable notification jingle. The IDs must match the
// tune keys in the client's @dc/jingle module.
type JingleOption struct {
	ID    string
	Label string
}

// PushDevice is one registered Web Push subscription shown on the settings
// page. Endpoint lets the dc-push-settings element recognize the row that
// belongs to the current device. Stale marks devices bound to older VAPID
// keys that can no longer receive pushes.
type PushDevice struct {
	ID       string
	Label    string
	Endpoint string
	Added    string
	Icon     string
	Stale    bool
}

// PushWebhook is one registered notification webhook.
type PushWebhook struct {
	ID  string
	URL string
}

// SettingsGeneralData feeds the general settings page.
type SettingsGeneralData struct {
	Page
	RestoreEnabled bool
}

// EditorIntelProfile is one language server row on the editor settings
// page.
type EditorIntelProfile struct {
	ID      string
	Label   string
	Command string
	Path    string
	Found   bool
	Enabled bool
}

// SettingsEditorData feeds the editor settings page.
type SettingsEditorData struct {
	Page
	Mode          string
	AutoAI        bool
	DebounceMs    int
	Profiles      []EditorIntelProfile
	OllamaEnabled bool
	OllamaModel   string
	Connections   int
}

// SettingsNotificationsData feeds the notifications settings page.
type SettingsNotificationsData struct {
	Page
	Jingles        []JingleOption
	Selected       string
	VAPIDPublicKey string
	Devices        []PushDevice
	StaleDevices   bool
	Webhooks       []PushWebhook
	BaseURL        string
}
