package render

import (
	"html/template"
	"strings"
)

// DocsData feeds the documentation page: one intro line plus the topics,
// each rendered as a section of title/description rows, listed in the column beside them.
type DocsData struct {
	Page
	Lead   string
	Topics []DocsTopic
}

// DocsTopic groups the documented behavior of one area of the app.
type DocsTopic struct {
	Key   string
	Title string
	Icon  string
	Lead  string
	// Intro is an optional paragraph rendered above the items.
	Intro template.HTML
	// LinkURL and LinkText render an optional action next to the title.
	LinkURL  string
	LinkText string
	Items    []DocsItem
}

// DocsItem is one documented control, gesture, or shortcut.
type DocsItem struct {
	Title string
	// Tag marks the context a control belongs to (Touch, Desktop, Coder).
	Tag      string
	TagClass string
	// Keys holds the keyboard alternatives, each a sequence of key caps.
	Keys []DocsKeys
	Desc template.HTML
}

// DocsKeys is one key combination, rendered as kbd caps joined by a plus.
type DocsKeys struct {
	Caps []string
}

// DocsCap is one rendered key cap. Caps named after a Tabler icon render as
// that glyph, so a pair like the arrow keys shares one font.
type DocsCap struct {
	Text string
	Icon string
}

// docsCapLabels gives the icon caps their accessible label.
var docsCapLabels = map[string]string{
	"ti-arrow-left":  "Left arrow",
	"ti-arrow-right": "Right arrow",
}

// Parts returns the caps of the combination in render order.
func (k DocsKeys) Parts() []DocsCap {
	parts := make([]DocsCap, len(k.Caps))
	for i, cap := range k.Caps {
		if strings.HasPrefix(cap, "ti-") {
			parts[i] = DocsCap{Icon: cap, Text: docsCapLabels[cap]}
			continue
		}
		parts[i] = DocsCap{Text: cap}
	}
	return parts
}

// HasKeys reports whether the item carries a keyboard shortcut.
func (i DocsItem) HasKeys() bool { return len(i.Keys) > 0 }

// Count returns the number of documented entries in the topic.
func (t DocsTopic) Count() int { return len(t.Items) }

// DocsLead is the note above the topics.
const DocsLead = "Shortcuts use Ctrl, and Cmd on a Mac where it is supported. Some are reserved by the browser and work only in the installed web app. Where the desktop right-clicks for a menu, touch long-presses, and scrolling cancels the press."

// DocsTopics returns the documentation content.
func DocsTopics() []DocsTopic {
	return []DocsTopic{
		{
			Key:   "navigation",
			Title: "Navigation",
			Icon:  "ti-navigation",
			Lead:  "Move between terminals, projects, and the editor.",
			Items: []DocsItem{
				{
					Title:    "The list sheet",
					Tag:      "Phone and tablet",
					TagClass: "bg-blue-lt",
					Desc:     `On a phone, Projects, Terminals and Assistants in the tab bar open the area's list as a sheet; a row opens its page and closes the sheet. Each list filters, projects also sort <i class="ti ti-arrows-sort align-text-bottom" aria-hidden="true"></i>, and a filter stays until you clear it. <i class="ti ti-dots align-text-bottom" aria-hidden="true"></i> holds a row's actions, the grip <i class="ti ti-grip-vertical align-text-bottom" aria-hidden="true"></i> drags it into a new order, and a split view lists its members under it.`,
				},
				{
					Title:    "Cockpit",
					Tag:      "Phone and tablet",
					TagClass: "bg-blue-lt",
					Desc:     `The last tab bar entry. Its gauge icon keeps the tab's color while CPU, RAM and disk stay below 80 percent, turns yellow from 80 and red from 95. A blue dot means an unread notification or a backup file to resolve. A tap opens a sheet as tall as the other lists: CPU, RAM and disk in one row (with a pointer, hover shows the numbers), the three newest notifications with Mark all read and Show all, tiles for theme (shows the mode in force), update (Up to date, or Update to the newer version, a tap checks now) and logout, the settings sections with the count of backup files to resolve, and the docs.`,
				},
				{
					Title: "The project row's menu",
					Desc:  `The three dots <i class="ti ti-dots align-text-bottom" aria-hidden="true"></i> on a project row, a right click, or a held finger: open the project or its editor, start a coder or a shell, edit its tags, git and compose actions, delete.`,
				},
				{
					Title: "Project tags",
					Desc:  `The dashed chip after a row's tags, or <em>Edit tags</em> in its menu: the project's tags come first, the cross takes one off, then type to find or create a tag or tap another one. A tag's name holds A to Z, a to z, 0 to 9, - and _, at most 40, and is the same in any case. A new tag takes the color the fewest tags wear. A board row names its tags above its terminals, past eight they fold behind <em>+N</em>. The list column and the switchers show one small disc, a slice per tag in its color, its tooltip names them. A worktree carries no tags. A backup carries them in the Projects section.`,
				},
				{
					Title: "Manage tags",
					Desc:  `Settings &rarr; Projects &rarr; Tags, or <i class="ti ti-pencil align-text-bottom" aria-hidden="true"></i> at the end of the tag bar: every tag with its color, its name and its projects, a tap on the number shows them on the board. <em>Add tag</em> makes a new one, one save at most 20. A tag lives on its own, without any project, until <em>Remove</em> takes it away. Nothing changes before <em>Save</em>, the footer counts the changes. Another tag's name merges the two, the row says into which, the result keeps that tag's color. <em>Remove</em> takes a tag off every project, <em>Undo</em> keeps it, a save that removes asks first. Selected tags merge, take a color or go at once. A save over tags changed elsewhere meanwhile changes nothing.`,
				},
				{
					Title: "Filter by tag",
					Desc:  `A tap on a tag in the bar under the board's filter, or on a row's tag, shows only the main projects carrying it and leaves their worktrees folded, more tags narrow further. Each tag counts the projects left that carry it, one that would leave none is dimmed. Picked tags stand in the filter field with a cross each, the field counts what is left and its one <i class="ti ti-x align-text-bottom" aria-hidden="true"></i> clears text and tags. On a desktop the bar keeps one line, <em>+N more</em> lists every tag. The phone's list sheet keeps a filter of its own. The text filters and both switchers match a tag's name too, a worktree through its main project.`,
				},
				{
					Title: "The terminal chips",
					Desc:  `A project row lists its coders and shells as chips, running ones first. Every running one always shows; past eight chips the inactive coders fold behind a <em>+N</em> chip.`,
				},
				{
					Title: "Git from the projects page",
					Desc:  `A repository row carries a git button <i class="ti ti-brand-git align-text-bottom" aria-hidden="true"></i>. <em>New worktree</em> opens the create form with this project as the source, only on a main repository. <em>Fetch</em> reports in a toast how far the checked out branch stands from its upstream. <em>Commit changes</em> and <em>Compare</em> open the editor on that view; switching the branch, push and pull stay in the editor's git sheet.`,
				},
				{
					Title: "Worktree post script",
					Desc:  `Settings &rarr; Projects &rarr; Worktrees holds one script that runs in every new worktree, its first line names the interpreter. It is stopped after 15 seconds. A failure notifies, the worktree stays.`,
				},
				{
					Title: "The list column keeps its width and place",
					Desc:  `On a desktop the list next to the rail drags wider or narrower at its right edge, a double click puts the default back. Width and scroll position are kept per area on this device.`,
				},
				{
					Title: "Open the terminal switcher",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Ctrl"}}},
					Desc:  `Tap Ctrl twice with no other key in between. Type to filter, the arrows move, Enter opens and Escape closes. It lists active terminals, every assistant by its name (the assistants overview only while there is none), resumable coders, project editors and the new-terminal actions. The filter matches everything a row shows.`,
				},
			},
		},
		docsAssistantsTopic(),
		{
			Key:   "terminals",
			Title: "Terminals",
			Icon:  "ti-terminal-2",
			Lead:  "Switching, splits, and the controls on a terminal page.",
			Items: []DocsItem{
				{
					Title: "Terminal input",
					Desc:  `Keystrokes go straight through to whatever runs in the terminal.`,
				},
				{
					Title: "Copy and paste",
					Desc:  `The copy button, <i class="ti ti-copy align-text-bottom" aria-hidden="true"></i> on a shell and <i class="ti ti-messages align-text-bottom" aria-hidden="true"></i> on a coder, shows the terminal's text in its place, to select and copy. On a shell that is its scrollback, how many lines is a choice in the head. On a coder it is the recorded conversation, the newest messages first and older ones on request, with a copy button per message, and <em>Screen</em> for what stands on the screen. <em>Copy all</em> takes everything shown, the text follows the terminal font size, and the cross or <kbd>Esc</kbd> brings the terminal back. Pasting <i class="ti ti-clipboard align-text-bottom" aria-hidden="true"></i> sends the clipboard as input. A copy a program makes, a coder's <code>/copy</code> for one, lands in the clipboard; where the browser refuses that, a toast offers <em>Copy</em>.`,
				},
				{
					Title: "Links",
					Desc:  `A click or a tap on a link a program prints, such as the file names in a coder's tool lines, opens it. A file inside a project opens in that project's editor, a web address in a new tab, anything else stays closed. A path of an existing file of the terminal's project links too, relative to its root or absolute, in the terminal, the copy view and the conversation, and <code>:line</code>, <code>:line:column</code> or a range <code>:line-line</code> after it places the cursor, on the first line of a range. A name without a dot or a slash, such as <code>Makefile</code>, links when a file of exactly that name stands in the project root. In the terminal and the copy view it links also when the terminal wraps it onto the next row, or a program at its margin, unless a row names a file on its own, and in the terminal a new word on the row the cursor stands on links once the cursor leaves it, one on a row whose text a program keeps replacing in place, a clock or a counter, once the row stood still for 1.5 seconds. A word without a letter, such as a time, a version or a size, never links. Inside the editor's terminal panel a file of its project opens in place. <kbd>Ctrl</kbd>, <kbd>Cmd</kbd> or <kbd>Shift</kbd> opens a file in a new tab.`,
				},
				{
					Title:    "Send files to a coder",
					Tag:      "Coder",
					TagClass: "bg-secondary-lt",
					Desc:     `Drop files onto a coder terminal or paste them there, then reference them in a prompt by copying their path from the files dialog. The upload button <i class="ti ti-upload align-text-bottom" aria-hidden="true"></i> in the coder footer opens that dialog.`,
				},
				{
					Title: "Refresh the stream",
					Desc:  `The refresh button <i class="ti ti-refresh align-text-bottom" aria-hidden="true"></i> in the footer reloads the terminal stream when the view looks out of sync.`,
				},
				{
					Title: "Font size and theme",
					Desc:  `The gear <i class="ti ti-settings align-text-bottom" aria-hidden="true"></i> sets the font size and the color theme, stored per device. Every palette follows the OS between a light and a dark variant.`,
				},
				{
					Title:    "Arrange tabs and panes",
					Tag:      "Desktop",
					TagClass: "bg-secondary-lt",
					Desc:     `Drag tabs to reorder them, and that order is shared across devices. Hold a tab briefly over the center of another to make a split. Drag a split pane by its head: sideways into another column stacks it there, up and down sorts it inside its column, and a drop on the split's left or right edge opens a column of its own.`,
				},
				{
					Title:    "New terminal inside a split",
					Tag:      "Desktop",
					TagClass: "bg-secondary-lt",
					Desc:     `A pane head's menu offers <em>New shell here</em> and <em>New coder here</em> into that pane's column; the same two in the split tab's menu open a column of their own on the right. The plus menu <i class="ti ti-plus align-text-bottom" aria-hidden="true"></i> keeps creating a standalone terminal everywhere.`,
				},
				{
					Title:    "Tab context menu",
					Tag:      "Desktop",
					TagClass: "bg-secondary-lt",
					Desc:     `Right-click a tab for rename, mark read, steer or release, project and editor links, ungroup, stop, and delete. Split pane heads and the chips on the projects page carry their own.`,
				},
				{
					Title:    "A new project needs no permission",
					Tag:      "Coder",
					TagClass: "bg-secondary-lt",
					Desc:     `A coder started in a project it has never seen would ask whether it may work on the files there. The cockpit marks the project trusted in the coder's own configuration beforehand, so it begins with the task.`,
				},
				{
					Title:    "Stop, resume and delete a coder",
					Tag:      "Coder",
					TagClass: "bg-secondary-lt",
					Desc:     `Stopping keeps the conversation: resume it from the + menu in the tab strip, the terminal switcher, or its project page. Delete sits next to stop in every menu and behind the swipe on a phone; it stops a running coder first, and the conversation is then gone and cannot be resumed.`,
				},
				{
					Title:    "Rename a coder",
					Tag:      "Coder",
					TagClass: "bg-secondary-lt",
					Desc:     `Rename sits where a shell's does, and a click on the coder's heading opens it too. The cockpit sends <code>/rename &lt;new name&gt;</code> into the running coder as is, regardless of its input line or an open dialog; if that does not fit, cancel and type the command yourself. An inactive coder cannot be renamed. OpenCode's own <code>/rename</code> takes no name, so for it the entry only says to run <code>/rename</code> in the coder and sends nothing.`,
				},
				{
					Title:    "On-screen controls",
					Tag:      "Touch",
					TagClass: "bg-blue-lt",
					Desc:     `On a phone a bar under the terminal sends Esc, Tab, Ctrl+C, Enter and Backspace, plus Shift+Tab in a coder, and Ctrl sends the next key with Ctrl held. Two buttons are dragged instead of tapped: the direction pad <i class="ti ti-arrows-move align-text-bottom" aria-hidden="true"></i> takes the arrow key from the way you drag and repeats it while you hold, the page pad <i class="ti ti-arrow-autofit-height align-text-bottom" aria-hidden="true"></i> pages the scroll history up and down and jumps to its ends sideways.`,
				},
				{
					Title:    "Swipe the terminal",
					Tag:      "Touch",
					TagClass: "bg-blue-lt",
					Desc:     `Both gestures live in a band across the middle of the terminal, roughly its middle half; the left and right edges stay for scrolling the page. Sideways moves to the next or previous terminal in tab order, wrapping at both ends, with a pill naming the destination. Up and down scrolls the history, and a fling keeps it moving.`,
				},
				{
					Title: "Step through open terminals",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Tab"}}, {Caps: []string{"Ctrl", "Shift", "Tab"}}},
					Desc:  `Steps through active terminals in tab order and wraps at either end.`,
				},
				{
					Title: "Open the plus menu",
					Keys:  []DocsKeys{{Caps: []string{"Cmd", "T"}}},
					Desc:  `Opens the plus menu <i class="ti ti-plus align-text-bottom" aria-hidden="true"></i> of the tab strip with the keyboard in it, so the arrows walk to a new coder, a new shell or a coder to resume and Enter takes it.`,
				},
				{
					Title: "Close the current terminal",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "X"}}},
					Desc:  `Stops a coder or deletes a shell, the way the cross on its tab does, and it asks first. On a split view it belongs to the tab, so it closes the whole split after one question.`,
				},
				{
					Title: "Choose the active split pane",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "ti-arrow-left"}}, {Caps: []string{"Ctrl", "Shift", "ti-arrow-right"}}},
					Desc:  `Moves the focus between panes, the columns left to right and each column top to bottom, wrapping at the ends. The active pane takes the keyboard and shows its own footer controls.`,
				},
				{
					Title: "Close one pane of a split",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "Backspace"}}},
					Desc:  `Closes the active pane and leaves the rest of the split standing, the way the cross in its head does, and it asks first. This one is Ctrl even on a Mac, where the Cmd version belongs to the browser.`,
				},
				{
					Title:    "Push and pull from a terminal",
					Tag:      "Coder",
					TagClass: "bg-secondary-lt",
					Desc:     `An ssh key with a passphrase has nobody to ask in a terminal, so <code>dev-cockpit git</code> runs any git command through the cockpit: same directory, git's own output and exit code, and the question reaches you as a dialog in the browser. Every installed coder gets a skill for it, listed under Settings &rarr; Coder &rarr; Skills as <span class="badge bg-secondary-lt">Managed</span> and not editable there.`,
				},
			},
		},
		{
			Key:   "editor",
			Title: "Project editor",
			Icon:  "ti-code",
			Lead:  "Shortcuts and controls in the project editor.",
			Items: []DocsItem{
				{
					Title: "Switch projects",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "P"}}},
					Desc:  `The project name above the file tree, or <em>Switch project</em> in the editor's menu, opens a palette: the last used projects first, then all of them, every row naming its repository and branch. Type to narrow by name, repository or branch, and Enter on a fresh palette goes back to the last used project. A project comes back exactly as it was left, the open tabs with their cursor and scroll included.`,
				},
				{
					Title: "The file tree",
					Desc:  `The folder button <i class="ti ti-folder align-text-bottom" aria-hidden="true"></i> folds the tree column away on a wide screen and opens the drawer on a small one, and the divider next to the tree resizes it. Fold, width, scroll and the open folders are remembered per project, and closing a folder folds everything inside it.`,
				},
				{
					Title: "Quick open",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "O"}}},
					Desc:  `Open any project file by name. Pressing bare Shift twice does the same.`,
				},
				{
					Title: "Go to definition and usages",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "B"}}, {Caps: []string{"Shift", "F12"}}},
					Desc:  `For PHP, Go, TypeScript and JavaScript. Hold <kbd>Ctrl</kbd> and click a symbol to jump to its definition, or to its usages when the cursor already sits on the declaration, and on touch a tap raises a small <em>Look up</em> action. A target outside the project opens too, a dependency's sources or a standard library, as a read only tab. PHP and Go index the project first, the statusbar showing how far, and a lookup waits for that index; <em>Reindex</em> in the editor's menu starts them over. A JavaScript or TypeScript project without a <code>jsconfig.json</code> or <code>tsconfig.json</code> gets a default one. Settings &rarr; Editor &rarr; LSP picks how each server runs. A project's servers stop after 300 seconds without editor use, minimum 60. At most 3 projects run servers at the same time, minimum 1, all languages of a project count as one. Another project stops the least recently used one, unless all are busy.`,
				},
				{
					Title: "Copy a class name",
					Desc:  `In PHP a right click on the name of a class, interface, trait or enum offers to copy its fully qualified name, the one an import or an alias stands for; anywhere else the browser's own menu stays. The language server is asked while the pointer rests on a name, so the entry is there once it answered, and not while it indexes. On touch the pill carries <em>Copy FQCN</em> on such a name.`,
				},
				{
					Title: "Git marks",
					Desc:  `In a git repository a changed file carries a letter on its tree row and its tab: <span class="text-green">A</span> added, <span class="text-cyan">U</span> untracked, <span class="text-yellow">M</span> modified, <span class="text-red">D</span> deleted, <span class="text-azure">R</span> renamed, <span class="text-red">!</span> conflicted, a folder a dot for the most pressing change under it. The open file marks its own changes against the last commit in the gutter, <span class="text-green">green</span> for a new line, <span class="text-azure">blue</span> for a changed one, a grey tick where lines went, following your typing without a save.`,
				},
				{
					Title: "Line comments",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Alt", "C"}}, {Caps: []string{"Ctrl", "Shift", "C"}}},
					Desc:  `Click a line number, or the gutter beside it, for the line's menu: add or edit the comment, delete it, copy the path with the line, relative or through <em>absolute</em> in full, toggle the git blame, and with the blame on, the line's commit. A right click and a long-press open the same menu. Comments stick to their line while you edit, follow a rename, and are saved with the project, so they reach your other devices. The quoted line is the anchor: moved and standing in the file exactly once the comment follows it, otherwise it reads outdated with the number orange and struck through. <em>Line comments</em> in the editor menu lists every one of the project, with <em>Go to line</em>, <em>Edit</em>, <em>Delete</em>, <em>Copy as text</em> and <em>Delete all comments</em>. Each entry of the list shows its path on one line, a control character or line separator as its escape, and its note on one line below. A comment a guest wrote through a shared link carries the link's name: in the line's menu, in the dialog, as the first line of its entry in the list with the person icon <i class="ti ti-user align-text-bottom" aria-hidden="true"></i>, and at the start of its entry in the text. The text quotes each note under its entry line by line as written, code too. A line break, a control character or a line separator shows as its escape in a path or a code line, and in a note any of them but a plain line break, so no line leaves its entry.`,
				},
				{
					Title: "Diff and blame a file",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Alt", "D"}}, {Caps: []string{"Ctrl", "Alt", "B"}}},
					Desc:  `Right-click a file, on its tab or its tree row: <em>Show git diff</em> puts it next to the last commit and <em>Show git blame</em> writes the day and the author of the last change next to every line, the newest commit bold and undimmed, and follows your typing with <em>uncommitted</em> on the lines you change; the line's menu then offers that commit: the file diffed against it, what it changed in the file, the revision comparison started on it, the file history and the hash. <em>Diff against revision</em> compares it with any branch, tag or commit instead, from where your branch split off from it, the way a merge request reads, and <em>File history</em> lists the commits that touched the file, each one opening the diff against that state. All of it is per file and comes back after a reload. To put two files on disk side by side, pick <em>Select for compare</em> on one and <em>Compare with</em> on the other.`,
				},
				{
					Title: "Commit from the editor",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "K"}}},
					Desc:  `The commit button <i class="ti ti-git-commit align-text-bottom" aria-hidden="true"></i> above the file tree, or <em>Commit</em> in the git sheet, turns the tree into the list of changes, each with a checkbox and nothing picked. Only the checked files go in, exactly as they stand in the working copy; whatever a coder has staged for other files stays staged. A row click shows its diff and <kbd>Ctrl</kbd>+<kbd>Enter</kbd> commits, <em>Amend</em> rewording or extending the last one. The message and the picks are saved with the project as you type, so another device opens the panel where you left it. The list groups by folder, a folder's checkbox picking everything below it, and <i class="ti ti-folders align-text-bottom" aria-hidden="true"></i> switches to a flat list. <em>Commit and push</em> behind the arrow pushes right after. <em>Open file</em> in a row's menu opens the file itself without a diff, a deleted one read only as the last commit has it.`,
				},
				{
					Title: "Compare from the editor",
					Desc:  `The compare button <i class="ti ti-git-compare align-text-bottom" aria-hidden="true"></i> above the file tree, or <em>Compare</em> in the git sheet, turns the tree into the list of files that differ between two revisions. <em>from</em> and <em>to</em> open the revision picker, a typed name such as <code>HEAD~3</code> works too, and the select switches between <code>a...b</code> (changes since the split, default) and <code>a..b</code> (all differences). The list behaves like the commit view. <em>to</em> starts on <em>Working copy</em>, the files on disk, untracked ones included, and the list follows them. <kbd>Enter</kbd> opens the read only diff of a file between the two revisions, or with the working copy the file itself, editable and diffed against the left side. <em>Open file</em> in a row's menu opens the file as the right side has it, the working copy without a diff, a revision read only, a deleted file as the left side had it.`,
				},
				{
					Title: "Revert a file or a folder",
					Desc:  `<em>Revert changes</em> in the menu of a changed file or folder in the tree, a file's tab, or a row of the commit list puts the path back to the last commit, staged edits included. A file the last commit does not hold cannot be restored, so reverting deletes it, which the confirmation says before anything runs. A reverted file reads the disk again, unsaved edits included.`,
				},
				{
					Title: "Branch, push and pull",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "G"}}},
					Desc:  `In a git repository the branch stands in the statusbar with how far it is ahead <span class="text-secondary">&uarr;</span> and behind <span class="text-secondary">&darr;</span> its upstream. Tapping it, or <em>Git</em> in the editor menu, opens the git sheet: switch branch (remote ones included, checking one out creates the local branch that tracks it), new branch, commit, push, pull, fetch, and the recent commits, each offering its diff, its hash and a tag. <em>Pull</em> only fast forwards and <em>Force push</em> runs as force-with-lease after asking; there is no stash and no merge here, and what git refuses comes back in git's own words. A switch or a pull never touches unsaved work. A remote that wants a passphrase asks in a dialog on every open page, and one device's answer closes it everywhere. A project that is no repository yet offers cloning into its folder instead.`,
				},
				{
					Title:    "File and tab menus",
					Tag:      "Editor",
					TagClass: "bg-secondary-lt",
					Desc:     `Right-click a tab or a tree row for everything that acts on that one file: copy, download, extract, rename, revert, delete, and how to look at it, the preview, the git diff, the diff against a revision, the file history, the blame and the two compare entries. On touch, long-press; tapping the active tab opens its menu too. <kbd>F2</kbd> renames the open file, <kbd>Ctrl</kbd>+<kbd>Alt</kbd>+<kbd>R</kbd> reveals it in the tree.`,
				},
				{
					Title: "One menu for everything else",
					Desc:  `Next to the tabs the toolbar keeps only the menu <i class="ti ti-dots-vertical align-text-bottom" aria-hidden="true"></i> and, while the open file is unsaved, Save. Everything else is in that menu and reads the same on a phone and on a wide screen: the open files, go to file, find in the file and in the project, go to line, the editor settings, save all, the git sheet, and the keyboard shortcuts.`,
				},
				{
					Title: "The sheets take the keyboard",
					Desc:  `The git sheet, the docker sheet, the open files and a file's history open with the first row marked: <kbd>&darr;</kbd> and <kbd>&uarr;</kbd> walk the rows and wrap, <kbd>Enter</kbd> runs the marked one, <kbd>Escape</kbd> leaves a level at a time and then the sheet. The branch and revision lists start in their filter field instead. On a wide screen the sheet takes three quarters of the editor at its right edge, on a phone the full width from the bottom.`,
				},
				{
					Title: "Open files and their order",
					Desc:  `<em>Open files</em> in the menu brings them up from the bottom, one row each with its folder under the name, the git letter in front and a dot when it is unsaved. A row goes to that file, the cross closes it, and the grip <i class="ti ti-grip-vertical align-text-bottom" aria-hidden="true"></i> drags it elsewhere; with a mouse the tabs themselves drag. It is one order either way and it stays on this device.`,
				},
				{
					Title: "Tab groups",
					Desc:  `Drag a tab onto the edge of a group to open a new group on that side, onto another group's tabs or its middle to move it there. The tab menu does the same with <em>Split right</em>, <em>left</em>, <em>down</em>, <em>up</em> and <em>Move to group</em>. Save and close act on the group you clicked last, <kbd>Ctrl</kbd>+<kbd>Tab</kbd> and <kbd>Ctrl</kbd>+<kbd>Shift</kbd>+<kbd>Tab</kbd> step through the tabs of every group, the top row first and each row from the left. Drag the line between two groups to resize them, a double click makes them equal. A file stands in one group at a time, and a group closes with its last tab. The layout stays per project on this device. <em>Open files</em> lists every group, on a desktop a drag between them moves the file. Splitting needs a desktop. A narrow window shows the focused group with the tabs of every group in its strip, a tap brings that tab's group to the front, and the layout stays as it was.`,
				},
				{
					Title:    "Swipe to the next file",
					Tag:      "Touch",
					TagClass: "bg-blue-lt",
					Desc:     `Swipe left or right on the text to go to the next or the previous open file, in the order the list shows, wrapping at both ends, and a pill names the file you would land on. It works while <em>Wrap long lines</em> is on; with wrapping off the text itself scrolls sideways and the gesture belongs to the code. Up and down stays scrolling either way, and while text is selected the swipe steps aside.`,
				},
				{
					Title: "Move files",
					Desc:  `Drag a file or a folder in the tree onto another folder, or onto empty tree space for the project root. A pill names the target folder, holding the pointer at the top or bottom edge scrolls the tree, and resting it on a closed folder opens that folder. Open tabs follow the new path, and a name that is already taken asks before it is replaced.`,
				},
				{
					Title: "Copy and paste files",
					Desc:  `The tree menu copies a file or a folder and pastes it into another folder; pasting into the folder it already sits in makes a numbered copy. The clipboard belongs to this browser alone.`,
				},
				{
					Title: "Copy a path or a file's text",
					Desc:  `The tab menu and the tree menu of a file copy its path inside the project, the chip <em>absolute</em> on that row its full path on this host (<kbd>&rarr;</kbd> reaches it), and next to it its contents as text, what is unsaved in the editor included. A file the editor does not open as text, an image or an archive, says so instead of copying anything.`,
				},
				{
					Title: "Upload files",
					Desc:  `Drop files or whole folders onto the file tree; dropping onto a folder puts them there, and a dropped folder keeps its structure. Pasting does the same, into the folder selected in the tree, and the tree menu uploads into the row's folder. A name that is already taken is listed before the upload starts, and replacing it needs one confirmation.`,
				},
				{
					Title: "Download a folder, extract an archive",
					Desc:  `The tree menu packs a folder into a <code>.tar.gz</code> and downloads it, which Windows, macOS and Linux all unpack with their built in tar. A <code>.tar</code>, <code>.tar.gz</code> or <code>.zip</code> carries an extract entry there and when you open it: it unpacks into a new folder beside the archive, so nothing existing is overwritten.`,
				},
				{
					Title: "The editor follows the disk",
					Desc:  `An open editor watches its open tabs and unfolded folders and follows what happens to them outside. A file a coder writes into reloads in its tab, cursor and scroll position kept. One with unsaved changes of your own is never touched, its tab marked <i class="ti ti-alert-triangle align-text-bottom text-warning align-text-bottom" aria-hidden="true"></i> instead, and a deleted file marks its tab <i class="ti ti-file-off align-text-bottom text-danger align-text-bottom" aria-hidden="true"></i> and stays open so the next save writes it again. It works without a git repository too. Settings &rarr; Editor &rarr; Files sets how often, and turns it off.`,
				},
				{
					Title: "A save never overwrites newer work",
					Desc:  `A file you opened is saved onto exactly that file. If a coder or git wrote it in the meantime, nothing is written and a dialog says so: <em>Reload</em> takes the disk into the editor and your unsaved changes are gone, <em>Cancel</em> keeps them. If the file was deleted, <em>Create again</em> writes what is in the editor as a new file. There is no way to force the save.`,
				},
				{
					Title: "Save",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "S"}}},
					Desc:  `Saves the current file. A changed file is also written one second after the last keystroke; Settings &rarr; Editor &rarr; Files switches that off.`,
				},
				{
					Title: "Find in the file",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "F"}}},
					Desc:  `Opens the find panel for the current file.`,
				},
				{
					Title: "Close the current file",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "X"}}},
					Desc:  `Closes the open tab, and an unsaved one asks first. The same shortcut closes a terminal on the attach pages.`,
				},
				{
					Title: "Find in files",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "F"}}},
					Desc:  `Searches the contents of every project file. The query matches literally; the <em>.*</em> control in the field switches to regular expressions and <em>Aa</em> minds case, patterns coming without delimiters and <code>^</code> and <code>$</code> anchoring at line boundaries. <kbd>Enter</kbd> jumps and closes; <kbd>Ctrl</kbd>+<kbd>Enter</kbd>, or a Ctrl-click on a row, opens the file in front and leaves the palette where it stood, the mark stepping on.`,
				},
				{
					Title: "Replace in files",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "H"}}},
					Desc:  `The same search with a second field, from the menu <i class="ti ti-dots-vertical align-text-bottom" aria-hidden="true"></i>, the shortcut, or a folder's tree menu: every row shows the line as it would read afterwards and carries a control that writes just that line, <kbd>Shift</kbd>+<kbd>Enter</kbd> doing it for the marked row, while the button names the whole job before it asks. A file you are holding unsaved stops the job with nothing written.`,
				},
				{
					Title: "Preview files",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Alt", "P"}}},
					Desc:  `<em>Show preview</em> <i class="ti ti-eye align-text-bottom" aria-hidden="true"></i> in a file's context menu puts a markdown or an SVG file next to its rendered form and follows what you type; the shortcut toggles it for the open file. It is per file and comes back after a reload. The markdown preview renders tables, task lists, alerts like <code>&gt; [!NOTE]</code> and raw HTML such as <code>&lt;details&gt;</code> folds, <code>&lt;kbd&gt;</code> and <code>&lt;br&gt;</code>. A fold keeps the state you gave it while you type, a link to a heading scrolls to it, a link with a full address opens in a new tab. A relative link, read from the file's folder or with a leading / from the project root, opens the file or reveals the folder in the tree, a <code>#heading</code> after a markdown file opens its preview there, and a relative image shows the project's file. Nothing in it runs: scripts, styles, forms, frames and event handlers are dropped, links and images keep only http, https, mailto or relative addresses. Images open in a viewer, video and audio in a player, everything else offers a download.`,
				},
				{
					Title: "Editor settings",
					Desc:  `<em>Editor settings</em> in the menu <i class="ti ti-dots-vertical align-text-bottom" aria-hidden="true"></i> sets tab width, indentation, font size, line wrapping, whether unchanged parts of a diff are folded, and how a diff looks: side by side, inline, or automatic, which picks by the window width. All of it stays on this device. A file covered by a project's .editorconfig takes its indentation from there and the control only shows it.`,
				},
				{
					Title: "Terminal panel",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "J"}}},
					Desc:  `<em>Terminal</em> in the menu <i class="ti ti-dots-vertical align-text-bottom" aria-hidden="true"></i>, the terminal icon <i class="ti ti-terminal-2 align-text-bottom" aria-hidden="true"></i> in the statusbar, or the shortcut opens the project's coders and shells below the code. Tabs switch between them, <i class="ti ti-plus align-text-bottom" aria-hidden="true"></i> starts a new one or resumes a stopped coder, and the keys, the copy view, the refresh and the file upload work like on the terminal pages. Open or closed, the active tab and the height are remembered per project. Desktop only.`,
				},
				{
					Title: "Docker view",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "D"}}},
					Desc:  `<em>Docker</em> in the menu <i class="ti ti-dots-vertical align-text-bottom" aria-hidden="true"></i>, the docker icon <i class="ti ti-brand-docker align-text-bottom" aria-hidden="true"></i> in the statusbar counting the project's running containers, or the shortcut opens what the project's compose menu offers: its addresses, the stack logs, the compose commands and the output of the last run, and below them every container. On a desktop a container's shell and logs open in the terminal panel instead of taking the page.`,
				},
			},
		},
		{
			Key:   "docker",
			Title: "Docker",
			Icon:  "ti-brand-docker",
			Lead:  "Each project's compose containers and the commands that drive them.",
			Intro: `One connection to the Docker daemon, following its event stream, so everything here is live without polling. A container belongs to the project whose directory its compose file was started in, and a machine without a reachable daemon shows none of this.`,
			Items: []DocsItem{
				{
					Title: "Container chips",
					Desc:  `Each compose container is a chip <i class="ti ti-brand-docker align-text-bottom" aria-hidden="true"></i> on its project's row, named by its compose service: green running, gray stopped, red a failing healthcheck. They stand in that order wherever they are listed, so the one that wants attention is at the front and never behind a fold. Starts and stops from the command line show up too.`,
				},
				{
					Title: "Container actions",
					Desc:  `A click or tap on a running container's chip opens a shell inside it. A right-click or a long-press opens its menu instead: the published ports, an <em>Open</em> entry per address, <em>Shell</em>, <em>Logs</em>, <em>Filter logs&hellip;</em>, <em>Start</em>, <em>Stop</em>, <em>Restart</em>; a stopped container has no shell, so a click opens the menu there. Shell and logs are cockpit terminals in the tab strip, and <i class="ti ti-file-text align-text-bottom" aria-hidden="true"></i> on the chip opens the logs with one tap.`,
				},
				{
					Title: "Where an Open entry goes",
					Desc:  `A published port is opened on the address this page was reached on, at that port: <em>Open :18088</em>. A container behind a reverse proxy publishes nothing and is reached by host name out of one of its labels: <em>Open app.example.com</em>, the routed addresses first, without a port, over the scheme this page uses. Which label carries it is configuration, under Settings &rsaquo; Docker: the label, with <code>*</code> for any part of it, and a regular expression with a <code>host</code> capture. One rule covers the traefik router labels out of the box, and each row says what it finds in the containers running right now.`,
				},
				{
					Title:    "Links of running stacks",
					Tag:      "Status line",
					TagClass: "bg-secondary-lt",
					Desc:     `While a stack runs, a chip next to the server bars counts the addresses its containers answer on and the projects they belong to. Its menu lists them by project and stack, in the project order picked on the projects page, routed hosts first, then ports ascending, each opening in a new tab. The project of the page you are on starts unfolded, on a page without one the project you used last; the projects you unfold or fold stay that way in this browser. The chip follows the daemon live and is gone while nothing runs. Wide screens with a mouse only.`,
				},
				{
					Title: "Compose actions",
					Desc:  `A project with a compose file carries a compose button <i class="ti ti-brand-docker align-text-bottom" aria-hidden="true"></i> next to its row actions, also while nothing runs yet. Its menu opens every address the project's containers answer on, follows the whole stack in one <em>Logs</em> terminal, and lists one entry per configured command. Each runs in the background, the chips following live and a notification telling when it finished or failed, and it keeps going when the cockpit restarts. The arrow on a run's output page leads back to the page it was opened from, from this menu and from a notification to the project's row. An assistant's run reports into its thread instead, see Assistants.`,
				},
				{
					Title: "What a run wrote",
					Desc:  `The menu entry above the commands opens the output of that stack's newest run, and so does the notification when it is over: the whole output, while it runs and afterwards, with the exit code at the top. <em>Cancel</em> ends a command that is still going, from any page and after a restart.`,
				},
				{
					Title: "Configuring docker",
					Desc:  `Settings &rsaquo; Docker holds the daemon and the commands. The daemon is resolved automatically, <code>DOCKER_HOST</code>, then the docker context, then the standard socket paths, and a host set here wins. Each command entry has an icon, a label, the command line, a timeout, and whether it asks first; the grip <i class="ti ti-grip-vertical align-text-bottom" aria-hidden="true"></i> drags it into a new place and the save stores the order the menu shows. It runs in the stack's directory, directly and not through a shell, so quotes group words and nothing else is interpreted, and <code>./deploy.sh</code> is looked for from there up to the project root. Removing every entry leaves ports and logs alone, and one button puts the defaults back.`,
				},
				{
					Title: "Deleting a project",
					Desc:  `A project whose containers the daemon still shows is emptied before its directory goes: every stack comes down with its volumes, on a fixed command that is not one of the configured ones. The row says <em>Deleting&hellip;</em> while it works, and a restart in the middle picks it up again. If a stack cannot be brought down the deletion stops and nothing is removed. A repository with linked worktrees takes the worktree projects with it, named in the confirm.`,
				},
			},
		},
		{
			Key:   "notifications",
			Title: "Notifications",
			Icon:  "ti-bell-ringing",
			Lead:  "When a coder or shell has news, and where it shows.",
			Intro: `Every notification reads the same way round: one line saying what happened, and below it which coder, shell, job or command it happened to and a piece of what was written. The title is always the same short sentence, so a stack of them reads at a glance, and the name stands where a phone gives it room. Each target has at most one unread entry, and follow-up signals within 30 seconds are grouped on purpose. Opening a visible coder, shell or split pane marks its news read everywhere. Terminal icons double as status lights: green busy, blue unread, gray idle.`,
			Items: []DocsItem{
				{
					Title: "Coders",
					Desc:  `Claude reports finished turns, questions and permission requests through its injected hooks. The same hooks drive the working ring on its icon, never the screen: it runs from the prompt to the end of the turn and stops while claude waits on a permission or a question. Copilot emits a terminal bell. OpenCode reports through a plugin the cockpit keeps in its config directory, which stays silent for coders the cockpit did not start.`,
				},
				{
					Title: "Shells",
					Desc:  `A command that runs for at least two seconds notifies when its prompt returns. Every shell starts with <code>PS0</code> and <code>PROMPT_COMMAND</code> set to see that, so an rc file overwriting them turns the notices off. A bell always counts as news, so use <code>printf '\a'</code> when a script needs attention.`,
				},
				{
					Title:    "The assistant",
					Tag:      "Sparkles",
					TagClass: "bg-blue-lt",
					Desc:     `The title says what it is, an answer or a job that ended done, blocked or expired or a trigger that fired. The line below opens with the job or the trigger, whole, and carries the first words of what the assistant sends you: an answer, a check's report, or the answer a trigger produced. An answer you asked for has nothing narrower to name, so that line opens with the assistant instead.`,
				},
				{
					Title: "In the browser",
					Desc:  `The bell <i class="ti ti-bell align-text-bottom" aria-hidden="true"></i>, the blue marks on terminals and projects, the browser title, a toast and the jingle all show the same unread notifications; the bell opens the list, where single entries or everything can be marked read. On a phone the Cockpit tab shows a dot and its sheet holds the list. Sound needs one browser interaction first, its volume is stored per device.`,
				},
				{
					Title:    "A steered coder stays quiet",
					Tag:      "Ownership",
					TagClass: "bg-blue-lt",
					Desc:     `While an assistant steers a job on a coder, that coder's own news rings nowhere: its report is what reaches you. The entry is still listed, already read.`,
				},
				{
					Title: "A git question",
					Desc:  `A <code>dev-cockpit git</code> command waiting for a passphrase is news like any other, one entry per place, so it reaches you with no cockpit page open, phone included. Opening any page shows the dialog, and the entry marks itself read once the dialog stands in front of you. The editor's own git actions raise none of this, you started them on a page that already shows the dialog.`,
				},
				{
					Title: "A failed worktree post script",
					Desc:  `One entry per worktree project, with the reason and the last lines of output. A script that passes rings nothing. Opening or deleting the project reads it.`,
				},
				{
					Title: "An approval",
					Desc:  `A compose command an assistant waits to run, or a project, coder or assistant it waits to delete, is news, one entry per question; the dialog shows on every page. It marks itself read once you decided or the question ran out.`,
				},
			},
		},
		{
			Key:      "push",
			Title:    "Push delivery",
			Icon:     "ti-send",
			Lead:     "Get the same notifications when the page is closed.",
			LinkURL:  "/settings/notifications#settings-webpush",
			LinkText: "Open settings",
			Items: []DocsItem{
				{
					Title: "When delivery happens",
					Desc:  `The server waits two seconds, then checks whether the target is still unread, so news you are already looking at sends no duplicate browser, phone or webhook alert.`,
				},
				{
					Title: "Web push",
					Desc:  `Enable it on each device in Settings &rarr; Notifications. It needs HTTPS and reaches you while the app is closed. On iPhone and iPad, install Dev Cockpit to the home screen first. A device marked <span class="badge bg-warning-lt">Old keys</span> must be enabled again from that device after push keys change.`,
				},
				{
					Title: "Webhooks",
					Desc:  `Each registered webhook receives JSON with <code>text</code>, <code>title</code>, <code>body</code> and <code>url</code>; Slack incoming webhooks work directly. Set the public base URL so outbound links point back to this cockpit.`,
				},
			},
		},
		{
			Key:      "costs",
			Title:    "Costs",
			Icon:     "ti-currency-dollar",
			Lead:     "What the coders spent, at API list prices.",
			LinkURL:  "/costs",
			LinkText: "Open costs",
			Intro:    `Experimental. Counts claude with Claude and Ollama Cloud models, at API list prices.`,
			Items: []DocsItem{
				{
					Title: "Where it shows",
					Desc:  `The status line shows today, a click opens the page. On a phone the Cockpit sheet links it with <i class="ti ti-currency-dollar align-text-bottom" aria-hidden="true"></i>.`,
				},
				{
					Title: "The page",
					Desc:  `Tiles for today and this month. Below them the period, then four charts of spend by project, by assistant, by coder and by model. <em>No project</em> is spend outside every project and assistant. Days follow the assistant time zone. The address holds the period, not the hidden series. Before anything is booked, it shows the same at $0.00.`,
				},
				{
					Title: "Period",
					Desc:  `Presets from today to the last 90 days, or picked days within the last 120 months. The arrows step one period back or forth. The bar stays pinned at the top while the charts scroll. The chart shows hours for one day, days up to 62 days, weeks up to 186 days, months beyond.`,
				},
				{
					Title: "Charts",
					Desc:  `Each chart stacks every series that spent in the period, the biggest of the period at the bottom of every bar, the header holds its total. Series too thin to show their color in a bar stack as one grey Other on top of it, the legend and the tooltip still list each. Hiding series rescales the axis to what is shown. By project shows all assistants as one series. The legend holds each series' total. A click shows only that series and again all, Ctrl, Cmd or Shift click or a long press hides or shows one. Hover or tap a bar for its numbers. A click on the bar opens its hours or days, on touch the link in the tooltip. A chart without spend shows its axes and one line.`,
				},
				{
					Title: "How it is booked",
					Desc:  `Every 10 seconds the cockpit reads the calls claude logs in its transcripts, sub-agents included, and books each at the list price of that moment. What claude's own total holds above its logged calls is booked on top. Kind and project come from the listed coders and the assistant workspace, names are read on every poll. Deleting a coder, project or transcript keeps what was booked. A claude started outside the cockpit counts as a coder. Spend of no listed coder, outside every assistant workspace, counts as other.`,
				},
				{
					Title: "How long it is kept",
					Desc:  `One file per month. Settings &rarr; Costs keeps 2 to 120 months, default 3, this month included. Older months are deleted. Hours stay for 30 days, older bookings count per day.`,
				},
				{
					Title: "Prices",
					Desc:  `Price tables are built in and refreshed daily from LiteLLM's public list and Ollama's pricing page, offline the last ones stay. Ollama Cloud models are priced per token, cache writes as input, off-peak rates outside 12:00 to 18:00 UTC on weekdays. Settings &rarr; Costs shows the tables, their fetches and the price per model. Local models count tokens only. A model that gets a price prices its earlier Ollama calls within the hour.`,
				},
			},
		},
		docsSharingTopic(),
		{
			Key:      "settings",
			Title:    "Settings and data",
			Icon:     "ti-settings",
			Lead:     "Appearance, optional behaviors, and moving your setup between hosts.",
			LinkURL:  "/settings/general",
			LinkText: "Open settings",
			Items: []DocsItem{
				{
					Title:    "Light and dark mode",
					Tag:      "Rail",
					TagClass: "bg-secondary-lt",
					Desc:     `The theme button at the foot of the rail, on a phone in the Cockpit sheet, steps through auto <i class="ti ti-contrast align-text-bottom" aria-hidden="true"></i>, light <i class="ti ti-sun align-text-bottom" aria-hidden="true"></i> and dark <i class="ti ti-moon align-text-bottom" aria-hidden="true"></i>. Auto follows the OS, the other two force one, stored per device. Page, editor and terminal follow together; coders may take up to two seconds.`,
				},
				{
					Title:    "How the machine is doing",
					Tag:      "Status line",
					TagClass: "bg-secondary-lt",
					Desc:     `Three bars in the status line show CPU, RAM and disk as percentages, green below 80, yellow from 80, red from 95. Hover shows a bar's name and value, a click opens the plain numbers. The disk is the one holding the projects. On Linux CPU is the share of cores at work since the last reading, on a Mac the load average against the core count, which can pass 100 percent. On a phone the Cockpit tab's gauge takes the worst of the three colors and its sheet shows the values.`,
				},
				{
					Title:    "Songs",
					Tag:      "Status line",
					TagClass: "bg-secondary-lt",
					Desc:     `On a desktop, the note <i class="ti ti-music align-text-bottom" aria-hidden="true"></i> in the status line opens the player. Play shuffles all genres, a genre's play only that genre, a title plays that song and then its genre. Next stays in that mode, and the music plays on across pages. Folds, filter, list position and volume stay per device, the volume apart from the notification sound.`,
				},
				{
					Title:    "Restore terminals at startup",
					Tag:      "Setting",
					TagClass: "bg-secondary-lt",
					Desc:     `Off by default. When on, a host reboot brings the working set back: coders resume, shells reopen empty in their project, and the tab order is kept. Ollama coders first wait up to a minute for their server.`,
				},
				{
					Title:    "Separate history per shell",
					Tag:      "Setting",
					TagClass: "bg-secondary-lt",
					Desc:     `Off by default. Gives every newly started shell its own command history instead of sharing the login shell's file; the history survives a restore.`,
				},
				{
					Title:    "Daily price refresh",
					Tag:      "Setting",
					TagClass: "bg-secondary-lt",
					Desc:     `On by default, in Settings &rarr; Costs. Off fetches no prices and keeps the last table.`,
				},
				{
					Title:    "The editor's two intervals",
					Tag:      "Setting",
					TagClass: "bg-secondary-lt",
					Desc:     `Settings &rarr; Editor &rarr; Git sets how often the server asks git for a change, in seconds, and when a file is big enough to ask before it is diffed. Files sets how often an open editor looks at what it has on the screen, zero turning it off. Two numbers on purpose: looking at a few open files costs nothing, asking git walks the whole working copy.`,
				},
				{
					Title:    "Coder instructions, agents and skills",
					Tag:      "Coder",
					TagClass: "bg-secondary-lt",
					Desc:     `Settings &rarr; Coder edits a coder's own files: its global instructions, its agents, and its skills, and holds its Models section. With more than one coder installed, the sidebar picks whose you edit.`,
				},
				{
					Title:    "Default models",
					Tag:      "Setting",
					TagClass: "bg-secondary-lt",
					Desc:     `Settings &rarr; Coder &rarr; Models holds a coder's start default, what a new coder and an assistant's chat run on when nothing is picked. Settings &rarr; Assistants &rarr; Models holds the Chat, Checks and Triggers picks a new assistant is made with, empty meaning the coder's default and Same as chat.`,
				},
				{
					Title:    "Claude through Ollama",
					Tag:      "Coder",
					TagClass: "bg-secondary-lt",
					Desc:     `A Claude coder or assistant picked as <code>ollama/&lt;name&gt;</code> runs through Ollama, shows an Ollama badge on its icon and needs <code>ollama signin</code> for cloud models. A cloud model gets the context window Ollama reports, unless <code>CLAUDE_CODE_MAX_CONTEXT_TOKENS</code> is already set. Outside the startup restore, such a coder starts or resumes only while the Ollama server answers, and model selects warn when it does not.`,
				},
				{
					Title:    "Ollama settings",
					Tag:      "Setting",
					TagClass: "bg-secondary-lt",
					Desc:     `Settings &rarr; Coder &rarr; Ollama, shown once ollama is installed, sets the Ollama host and lists the server's cloud models, Ollama's catalog and names added by hand.`,
				},
				{
					Title:    "The claude status line",
					Tag:      "Coder",
					TagClass: "bg-secondary-lt",
					Desc:     `Settings &rarr; Coder &rarr; Status line builds the line under claude's prompt from a list of values, separators and line breaks. A preset fills the list, Save keeps it. A number is colored by bounds, the highest one reached wins. A value without an answer leaves the line with the separator before it. A command runs in the coder's folder without a shell, gets claude's status JSON on stdin and is dropped after five seconds. Only <em>Weekly, one model</em> calls the usage API. The mode is read when a coder starts or resumes, the list at its next redraw, at most ten seconds later. <em>If ~/.claude/settings.json sets none</em>, the default, checks only that file. Claude's own settings files are never written.`,
				},
				{
					Title:    "Back up and move your setup",
					Tag:      "Data",
					TagClass: "bg-secondary-lt",
					Desc:     `Export the cockpit state to a file under Settings &rarr; Backup and import it on another host. Archives can be password-encrypted, and clashing files are shown for review before they overwrite.`,
				},
			},
		},
	}
}
