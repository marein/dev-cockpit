package render

// SplitMember is one terminal pane on the split view page.
type SplitMember struct {
	ID            string
	Name          string
	Kind          string // "coder" or "shell"
	Coder         string // owning coder id, empty for shells
	Project       string
	URL           string // the member's own attach page
	StreamURL     string
	ResizeURL     string
	InputURL      string
	ScrollHistory bool // shells scroll the tmux history
	// FilesData feeds the member's own files modal; nil for shells. The
	// active pane's contextual footer opens it through a per-member modal id.
	FilesData *CoderFilesData
}

// SplitAttachData is the model for the split view attach page.
type SplitAttachData struct {
	Page
	GroupID     string
	GroupName   string
	ProjectName string // the members' shared project, empty when they differ
	// Focus is the member whose pane starts active (a member link redirects
	// here carrying ?focus); defaults to the first member. FocusExplicit
	// tells the client a ?focus was requested, so it must not restore the
	// remembered pane over it.
	Focus         string
	FocusExplicit bool
	Members       []SplitMember
}
