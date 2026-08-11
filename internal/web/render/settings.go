package render

// SettingsNav feeds the settings sidebar, the one navigation every settings
// page shares (`settings_nav.gohtml`). Active names the entry to mark
// ("general", "editor", "docker", "notifications", "coder", "backup"). The coder pages are
// settings of one coder, so the sidebar picks the coder first and the page
// then shows that coder's sections: with several coders active the entry
// becomes one row per coder (Selected marks it), a single coder host keeps
// one plain Coder row pointing at Home.
type SettingsNav struct {
	Active   string
	Coders   []SettingsCoder
	Selected string // coder id the page is scoped to, empty off the coder pages
	Section  string // active coder section: "instructions" | "agents" | "skills" | "account"
	Reviews  int    // open backup overwrite reviews, badge on the backup entry
	Account  bool   // whether the page's coder has the account section at all
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

// SettingsDockerData feeds the docker settings page: which daemon the cockpit
// talks to, and the compose commands its menus offer.
type SettingsDockerData struct {
	Page
	SettingsNav SettingsNav
	// DockerHost is the stored docker-host setting, empty means resolve
	// automatically. DockerStatus is the current connection line under the
	// field, DockerConnected colors it.
	DockerHost      string
	DockerStatus    string
	DockerConnected bool
	// Actions is the configured compose commands, each with the argv it
	// splits into, so the form shows what a line really becomes. Empty means
	// no buttons anywhere, and the page then offers the defaults back.
	Actions []DockerActionRow
	// Icons is the icon vocabulary an entry picks from, name and picture.
	Icons []DockerIcon
	// LinkRules is where a container's own address comes from, each rule with
	// what it finds in what runs right now. Empty means the menus offer the
	// published ports and nothing else.
	LinkRules []DockerLinkRuleRow
	// LinkSchemes is what a rule may pin its links to, the empty one being
	// the page's own scheme.
	LinkSchemes []string
}

// SettingsEditorData feeds the editor settings pages, one per tab. The values
// are the effective ones, so a form always shows what applies, not what happens
// to be stored. Section marks the open tab the way the coder pages mark theirs,
// so the page can grow more of them.
type SettingsEditorData struct {
	Page
	SettingsNav    SettingsNav
	Section        string
	GitPollSeconds int
	DiffMaxLines   int
	DiffMaxKiB     int
	// Exclusions is the newline joined folder list the search tab edits.
	Exclusions string
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
