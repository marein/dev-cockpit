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
	HistoryEnabled bool
}

// BackupSection is one selectable export section on the backup page.
type BackupSection struct {
	ID          string
	Label       string
	Description string
	Available   bool
}

// BackupGroup groups the export sections on the backup page.
type BackupGroup struct {
	Label    string
	Sections []BackupSection
}

// BackupImportSection is one section found in an uploaded backup archive.
type BackupImportSection struct {
	ID          string
	Label       string
	Description string
	Files       int
	Size        string
	Supported   bool
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

// SettingsBackupData feeds the backup settings page. Tab picks the visible
// pane, "export" or "import", resolved server side so every redirect flow
// lands on the right one.
type SettingsBackupData struct {
	Page
	Tab             string
	Groups          []BackupGroup
	Import          *BackupImport
	ImportError     string
	Review          []BackupReviewRow
	ReviewMoreCount int
}

// BackupDiffLine is one rendered diff line on the merge page.
type BackupDiffLine struct {
	Kind string
	Text string
}

// BackupMergeData feeds the merge page for one overwritten file. Restart
// marks cockpit files, saving a merge or restoring restarts the server.
type BackupMergeData struct {
	Page
	ID       string
	FilePath string
	Text     bool
	HasDiff  bool
	Diff     []BackupDiffLine
	Content  string
	Previous string
	Restart  bool
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
