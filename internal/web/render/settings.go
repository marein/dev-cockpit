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
