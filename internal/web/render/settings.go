package render

// SettingsNav feeds the settings sidebar, the one navigation every settings
// page shares (`settings_nav.gohtml`). Active names the entry to mark
// ("general", "notifications", "coder", "backup"). The coder pages are
// settings of one coder, so the sidebar picks the coder first and the page
// then shows that coder's sections: with several coders active the entry
// becomes one row per coder (Selected marks it), a single coder host keeps
// one plain Coder row pointing at Home.
type SettingsNav struct {
	Active   string
	Coders   []SettingsCoder
	Selected string // coder id the page is scoped to, empty off the coder pages
	Section  string // active coder section: "instructions" | "agents" | "skills"
	Reviews  int    // open backup overwrite reviews, badge on the backup entry
}

// SettingsCoder is one coder row in the settings sidebar. URL keeps the
// section the page is on, so switching the coder stays in the same section.
type SettingsCoder struct {
	ID  string
	URL string
}

// Multi reports whether the coder entry splits into one row per coder. Single
// coder hosts keep the plain Coder row, like every other adaptive surface.
func (n SettingsNav) Multi() bool { return len(n.Coders) > 1 }

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
	SettingsNav    SettingsNav
	RestoreEnabled bool
	HistoryEnabled bool
}

// BackupSection is one selectable export section on the backup form.
// Requires carries the space joined section ids this one depends on, for
// the dc-backup-sections dependency enforcement; RequiresLabels the same
// dependencies as human labels, rendered under the checkbox so the
// relationship is visible before anything toggles.
// Detail carries an extra enumeration line under the checkbox, the dotfiles
// section lists the files it discovered there.
type BackupSection struct {
	ID             string
	Label          string
	Description    string
	Detail         string
	Available      bool
	Requires       string
	RequiresLabels string
}

// BackupGroup groups the export sections on the backup page.
type BackupGroup struct {
	Label    string
	Sections []BackupSection
}

// BackupImportSection is one section found in an uploaded backup archive.
// Requires and RequiresLabels drive the same dependency enforcement and
// hint as the export form, limited to the sections the archive contains.
type BackupImportSection struct {
	ID             string
	Label          string
	Description    string
	Files          int
	Size           string
	Supported      bool
	Requires       string
	RequiresLabels string
}

// BackupImport describes an uploaded archive waiting for the import
// selection, the adaptive part of the import flow.
type BackupImport struct {
	Token    string
	Created  string
	Host     string
	Version  string
	Sections []BackupImportSection
}

// BackupReviewRow is one overwritten file awaiting a keep, restore, or
// merge decision. It carries the CSRF token so the shared row template can
// render its action forms without extra template helpers. Restart marks
// cockpit files, restoring or merging one restarts the server.
type BackupReviewRow struct {
	ID        string
	Path      string
	CSRFToken string
	Restart   bool
}

// BackupRow is one stored backup in the export tab list.
type BackupRow struct {
	ID       string
	Name     string
	Created  string
	Size     string
	Sections int
	Running  bool
	Done     bool
	Error    string
}

// SettingsBackupData feeds the backup settings page. Tab picks the visible
// pane, "export" or "import", resolved server side so every redirect flow
// lands on the right one.
type SettingsBackupData struct {
	Page
	SettingsNav     SettingsNav
	Tab             string
	Backups         []BackupRow
	Import          *BackupImport
	ImportError     string
	Review          []BackupReviewRow
	ReviewMoreCount int
}

// SettingsBackupNewData feeds the create backup form page.
type SettingsBackupNewData struct {
	Page
	Groups []BackupGroup
}

// BackupListData feeds the standalone backup list fragment, pulled live by
// dc-backup-list. It carries no flash, so a live refresh never eats the
// redirect flash of a create or delete.
type BackupListData struct {
	Backups   []BackupRow
	CSRFToken string
}

// BackupMergeData feeds the merge page for one overwritten file. Restart
// marks cockpit files, saving a merge or restoring restarts the server.
type BackupMergeData struct {
	Page
	ID       string
	FilePath string
	Text     bool
	Content  string
	Previous string
	Restart  bool
}

// TelegramSettings feeds the Telegram section of the assistant settings page.
// The bot token is never part of it, only whether one is stored: it is a
// bearer credential and never goes back to a browser.
type TelegramSettings struct {
	TokenSet bool
	Enabled  bool
	// Status is the poller in one sentence: running, off, or stopped with the
	// reason, so a silent bot is looked up here and not on the phone.
	Status string
	// Stopped marks the state that needs the user, so the sentence can carry a
	// warning instead of reading like an ordinary line.
	Stopped  bool
	ChatName string
	ChatID   int64
	Paired   string
	// Code is the pairing code waiting to be sent to the bot, empty when none
	// was created or the last one ran out.
	Code string
	// CodeExpires is how long that code is still good for, "8 minutes".
	CodeExpires string
	// AnswersFromTelegram and ReportsFromTelegram are the two narrow choices,
	// each false when everything goes out. They decide what the bot sends and
	// nothing about the conversation itself.
	AnswersFromTelegram bool
	ReportsFromTelegram bool
}

// SettingsSection is one block of the assistant settings page. The page is
// rendered from a list of these, not from blocks written one after another in
// the template: the assistant is getting more settings, and a new one has to be
// an entry plus its own partial, never a rebuild of the page.
//
// Every section owns its form and its POST target, so saving one cannot touch
// the values of another, and a section whose precondition is missing says so in
// its own block instead of disappearing.
type SettingsSection struct {
	// ID is the anchor of the block, "telegram" renders as #settings-telegram.
	ID    string
	Title string
	Lead  string
	// Template is the partial that renders the block's body.
	Template string
	// Action is the section's own POST target, e.g. /settings/assistant/telegram.
	Action string
	// Missing says why this section cannot be used right now, empty when it can.
	Missing string
	// Data is what the partial renders, the section's own view model.
	Data any
	// CSRFToken and Flash are what a partial needs from the page: its forms
	// carry the token, and the flash of the section that was just saved is
	// rendered inside that section's own block.
	CSRFToken string
	Flash     Flash
	ShowFlash bool
}

// SettingsAssistantData feeds the assistant settings page.
type SettingsAssistantData struct {
	Page
	SettingsNav SettingsNav
	Sections    []SettingsSection
}

// SettingsNotificationsData feeds the notifications settings page.
type SettingsNotificationsData struct {
	Page
	SettingsNav    SettingsNav
	Jingles        []JingleOption
	Selected       string
	VAPIDPublicKey string
	Devices        []PushDevice
	StaleDevices   bool
	Webhooks       []PushWebhook
	BaseURL        string
}
