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
	// Col is the rendered column, 1 based and left to right, and Row/RowSpan
	// place the pane inside that column's stack. The three are the pane's
	// place in the page's one grid: the panes stay flat siblings so a layout
	// change is a style change and never a DOM move, which is what keeps the
	// streams connected. Order is the visual reading of that grid, columns
	// left to right and top to bottom, rendered as the pane's `order` style:
	// the keyboard stepping walks it, and it can differ from the flat member
	// order the strip surfaces render. Computed by splitLayout, mirrored by
	// terminal-split.
	Col     int
	Row     int
	RowSpan int
	Order   int
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
	// Cols and Rows are the grid tracks the panes are placed on: one column
	// per rendered column, and enough equal rows that every column divides
	// them evenly (a column of two panes and one of three share six rows).
	Cols int
	Rows int
}
