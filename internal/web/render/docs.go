package render

import (
	"html/template"
	"strings"
)

// DocsData feeds the documentation page: one intro line plus the topics,
// each rendered as a collapsible panel of title/description rows.
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
const DocsLead = "The whole app follows the OS light and dark mode. Shortcuts use Ctrl; where supported, Cmd (on Mac) works too. Some shortcuts are reserved by the browser and only work in the installed web app. Where the desktop right-clicks for a menu, touch long-presses, and scrolling cancels the press."

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
					Title:    "Quick navigation",
					Tag:      "Phone and tablet",
					TagClass: "bg-blue-lt",
					Desc:     `On phones and tablets, where it is the primary way to move around: the floating grid button <i class="ti ti-layout-grid align-text-bottom" aria-hidden="true"></i> browses active terminals and projects, and a project's list also starts a new coder or shell in that project. On a desktop its corner belongs to the assistant instead, and the terminal switcher (double Ctrl) is the route that leverages the keyboard.`,
				},
				{
					Title: "Quick-nav reorder and split",
					Desc:  `Drag rows to reorder terminals, on touch with the grip handle <i class="ti ti-grip-vertical align-text-bottom" aria-hidden="true"></i>. Hold a row over another row to group them into a split.`,
				},
				{
					Title:    "Quick-nav swipe actions",
					Tag:      "Touch",
					TagClass: "bg-blue-lt",
					Desc:     `Swipe a row left to reveal its actions, always in the same order: rename and ungroup first, then stop, then delete.`,
				},
				{
					Title: "Open the terminal switcher",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Ctrl"}}},
					Desc:  `Tap Ctrl twice without another key in between. Type to filter; use the arrows to move; Enter opens the selection and Escape closes it. The palette includes active terminals, the assistant, resumable coders, project editors, and new-terminal actions.`,
				},
			},
		},
		{
			Key:   "assistant",
			Title: "Assistant",
			Icon:  "ti-sparkles",
			Lead:  "Your own conversation with the cockpit, on every device.",
			Items: []DocsItem{
				{
					Title: "One place, always open",
					Desc:  `The sparkle button <i class="ti ti-sparkles align-text-bottom" aria-hidden="true"></i> opens the assistant in the header, the quick nav, the + menu of the tab strip and the terminal switcher. On a desktop it docks as a side panel, drag its left edge to resize, stored per device, and its round corner button takes the quick nav's place.`,
				},
				{
					Title:    "It sees the cockpit",
					Tag:      "Live state",
					TagClass: "bg-blue-lt",
					Desc:     `Ask what is running, what is waiting for you, or what happened while you were away: it reads the coders, the shells, the projects, the unread notifications, and what each of them last did. It types into a coder for you too, a prompt or the keys a dialog needs. A shell it can only read.`,
				},
				{
					Title:    "It remembers you",
					Tag:      "Memory",
					TagClass: "bg-blue-lt",
					Desc:     `Say remember that and the assistant writes it down. The brain icon <i class="ti ti-brain align-text-bottom" aria-hidden="true"></i> opens what it knows, to read and to correct. It goes into every answer, so it survives a new conversation.`,
				},
				{
					Title:    "Pictures, recordings and clips",
					Tag:      "Files",
					TagClass: "bg-blue-lt",
					Desc:     `Drop files onto the assistant or paste them into the message box; the paperclip <i class="ti ti-paperclip align-text-bottom" aria-hidden="true"></i> does the same.`,
				},
				{
					Title: "Starting over",
					Desc:  `The new-conversation button <i class="ti ti-message-plus align-text-bottom" aria-hidden="true"></i> starts fresh, and with more than one coder installed it asks which one answers. One conversation is live at a time, the earlier ones stay read-only under the clock icon <i class="ti ti-history align-text-bottom" aria-hidden="true"></i>.`,
				},
				{
					Title:    "How full the conversation is",
					Tag:      "Context",
					TagClass: "bg-blue-lt",
					Desc:     `A ring around the new-conversation button <i class="ti ti-message-plus align-text-bottom" aria-hidden="true"></i> fills with how much of the coder's context window the conversation takes up, orange from 85 percent, red from 95. It moves once per answer and stays empty for a model whose window this cockpit does not know.`,
				},
				{
					Title:    "What it may do",
					Tag:      "Tools",
					TagClass: "bg-blue-lt",
					Desc:     `It has the tools of a coder: reading and searching, writing files, running commands, fetching a page from the web, on that coder's default model. It belongs to no project, its own files stay in a workspace next to the cockpit data. A finished answer notifies like any other news, and reads itself while the assistant is open in front of you.`,
				},
				{
					Title:    "It hands work over",
					Tag:      "Coordinates",
					TagClass: "bg-blue-lt",
					Desc:     `Changes in your projects are not its job: it starts a coder for the task, briefs it, steers the job from the start, and tells you which coder is on it. It also resumes, stops and deletes coders, and creates and deletes projects.`,
				},
				{
					Title:    "It steers a job to the end",
					Tag:      "Wakes up",
					TagClass: "bg-blue-lt",
					Desc:     `A job is one coder, one task, and one criterion that decides the task is done. Work the assistant hands over is steered from the start, and you can steer any coder yourself. A steered coder that finishes, asks something or stops moving buys a check, and the check sends the coder what it needs to go on. Only done and blocked reach you. Ten checks and eight hours per job.`,
				},
				{
					Title:    "The steered coders",
					Tag:      "Coders",
					TagClass: "bg-blue-lt",
					Desc:     `The steering-wheel button <i class="ti ti-steering-wheel align-text-bottom" aria-hidden="true"></i> in the assistant's head opens the steered coders, its badge counts the ones still steered. A steered coder names its criterion, the last report, the checks it used and until when it runs, and takes the same criterion again once it is over.`,
				},
				{
					Title:    "Steer and release where the coder is",
					Tag:      "Ownership",
					TagClass: "bg-blue-lt",
					Desc:     `A steered coder's icon turns purple wherever it shows, and the color follows steer and release without a reload. Steer and release where the coder is listed: swipe its row in the quick nav, or open the context menu of its tab, its pane header or its chip. The dialog's criterion may stay empty, the checks then judge against the task the coder is on.`,
				},
			},
		},
		{
			Key:   "terminals",
			Title: "Terminals",
			Icon:  "ti-terminal-2",
			Lead:  "Switching, splits, and the controls on a terminal page.",
			Items: []DocsItem{
				{
					Title: "Terminal input",
					Desc:  `The terminal passes your keystrokes straight through to whatever runs in it.`,
				},
				{
					Title: "Copy and paste",
					Desc:  `Copy a selection with Ctrl/Cmd+C; on a phone, toggle copy mode <i class="ti ti-copy align-text-bottom" aria-hidden="true"></i> to select text with your finger. Pasting <i class="ti ti-clipboard align-text-bottom" aria-hidden="true"></i> sends the clipboard as input.`,
				},
				{
					Title:    "Coder prompt",
					Tag:      "Coder",
					TagClass: "bg-secondary-lt",
					Desc:     `The pencil button <i class="ti ti-pencil align-text-bottom" aria-hidden="true"></i> in the footer opens a box for composing a longer prompt and sends it to the coder.`,
				},
				{
					Title:    "Send files to a coder",
					Tag:      "Coder",
					TagClass: "bg-secondary-lt",
					Desc:     `Drop files onto a coder terminal or paste them there to upload them, then reference them in a prompt by copying their path from the files dialog. The upload button <i class="ti ti-upload align-text-bottom" aria-hidden="true"></i> in the coder footer opens the files dialog, which asks for the files and sends them as soon as they are picked or pasted.`,
				},
				{
					Title: "Refresh the stream",
					Desc:  `The refresh button <i class="ti ti-refresh align-text-bottom" aria-hidden="true"></i> in the footer reloads the terminal stream if the view looks out of sync.`,
				},
				{
					Title: "Font size, rows, and theme",
					Desc:  `The gear button <i class="ti ti-settings align-text-bottom" aria-hidden="true"></i> on a terminal page sets the font size, the visible rows, and the color theme, stored per device. Every palette follows the OS between a light and a dark variant. In a split view the rows are the height of the page, not of every pane: a column shows about that many lines in total, and stacked panes share them.`,
				},
				{
					Title:    "Arrange tabs and panes",
					Tag:      "Desktop",
					TagClass: "bg-secondary-lt",
					Desc:     `Drag tabs to reorder them; the order is shared across devices. Hold a tab over the center of another tab briefly to make a split. Drag a split pane by its head: sideways into another column stacks it there, up and down sorts it inside its column, and a drop on the left or right edge of the split opens a column of its own.`,
				},
				{
					Title:    "New terminal inside a split",
					Tag:      "Desktop",
					TagClass: "bg-secondary-lt",
					Desc:     `The context menu of a pane head offers <em>New shell here</em> and <em>New coder here</em>: the new terminal joins that pane's column. The same two entries in the split tab's menu open a column of its own on the right. The plus menu <i class="ti ti-plus align-text-bottom" aria-hidden="true"></i> keeps creating a standalone terminal everywhere, a split page included.`,
				},
				{
					Title:    "Tab context menu",
					Tag:      "Desktop",
					TagClass: "bg-secondary-lt",
					Desc:     `Right-click a tab for rename, mark read, steer or release, project and editor links, ungroup, stop, and delete. Split pane headers and the chips on the projects page carry their own menu.`,
				},
				{
					Title:    "A new project needs no permission",
					Tag:      "Coder",
					TagClass: "bg-secondary-lt",
					Desc:     `A coder started in a project it has never seen would ask whether it may work on the files there. The cockpit answers that beforehand, by marking the project as trusted in the coder's own configuration, so a coder that gets a task begins with the task.`,
				},
				{
					Title:    "Resume a coder",
					Tag:      "Coder",
					TagClass: "bg-secondary-lt",
					Desc:     `Stopping a coder keeps its conversation. Resume it from the + menu in the tab strip, the terminal switcher, or its project page.`,
				},
				{
					Title:    "Delete a coder",
					Tag:      "Coder",
					TagClass: "bg-secondary-lt",
					Desc:     `Delete sits next to stop in every menu and behind the swipe on a phone. A running coder is stopped first, then its conversation is deleted and cannot be resumed.`,
				},
				{
					Title:    "On-screen controls",
					Tag:      "Touch",
					TagClass: "bg-blue-lt",
					Desc:     `On a phone, a bar under the terminal sends common control keys: Esc, Tab, Ctrl+C, Enter, and Backspace, plus Shift+Tab in a coder. The Ctrl button sends the next key with Ctrl held. Two buttons are pressed and dragged instead of tapped: the direction pad <i class="ti ti-arrows-move align-text-bottom" aria-hidden="true"></i> takes the arrow key from the way you drag and keeps repeating it while you hold, and the page pad <i class="ti ti-arrow-autofit-height align-text-bottom" aria-hidden="true"></i> pages through the scroll history when dragged up or down, and jumps to its top or bottom when dragged left or right.`,
				},
				{
					Title:    "Swipe between terminals",
					Tag:      "Touch",
					TagClass: "bg-blue-lt",
					Desc:     `Swipe horizontally across the middle of the terminal to move to the next or previous terminal in tab order, wrapping at either end. The target pill previews the destination, and split members are part of the rotation.`,
				},
				{
					Title:    "Scroll the history",
					Tag:      "Touch",
					TagClass: "bg-blue-lt",
					Desc:     `Swipe vertically over the terminal to scroll its history; a fling keeps it moving. This gesture and the horizontal swipe between terminals both live in a band across the middle of the terminal, roughly its middle half; the left and right edges stay reserved for scrolling the page.`,
				},
				{
					Title: "Step through open terminals",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Tab"}}, {Caps: []string{"Ctrl", "Shift", "Tab"}}},
					Desc:  `Moves through active terminals in tab order and wraps at either end. Browsers often reserve this shortcut; it is reliable in an installed web app.`,
				},
				{
					Title: "Open the plus menu",
					Keys:  []DocsKeys{{Caps: []string{"Cmd", "T"}}},
					Desc:  `Opens the plus menu <i class="ti ti-plus align-text-bottom" aria-hidden="true"></i> of the tab strip and puts the keyboard in it, so the arrow keys walk to a new coder, a new shell or a coder to resume and Enter takes it.`,
				},
				{
					Title: "Close the current terminal",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "X"}}},
					Desc:  `Stops a coder or deletes a shell, the same way the cross on its tab does, and it asks before it happens. On a split view it belongs to the tab, so it closes the whole split after one question.`,
				},
				{
					Title: "Fullscreen terminal",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "F"}}, {Caps: []string{"Ctrl", "Shift", "Enter"}}},
					Desc:  `<em>Fullscreen</em> in the terminal settings <i class="ti ti-settings align-text-bottom" aria-hidden="true"></i> of the tab strip. Double-clicking unused space in that strip does the same thing.`,
				},
				{
					Title: "Choose the active split pane",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "ti-arrow-left"}}, {Caps: []string{"Ctrl", "Shift", "ti-arrow-right"}}},
					Desc:  `Moves focus between panes in a split terminal, walking the columns top to bottom and left to right, wrapping at the ends. The active pane receives keyboard input and shows its matching footer controls.`,
				},
				{
					Title: "Close one pane of a split",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "Backspace"}}},
					Desc:  `Closes the active pane and leaves the rest of the split standing, the same way the cross in its head does, and it asks first. This one is Ctrl even on a Mac, where the Cmd version belongs to the browser.`,
				},
				{
					Title:    "Push and pull from a terminal",
					Tag:      "Coder",
					TagClass: "bg-secondary-lt",
					Desc:     `An ssh key with a passphrase has nobody to ask in a terminal, so <code>dev-cockpit git</code> runs any git command through the cockpit, <code>push</code> and <code>pull</code> like everything else: same directory, git's own output and exit code, and the question reaches you as a dialog in the browser and as a notification. Every installed coder gets a skill for it that the cockpit writes itself and removes again at stop, listed under Settings &rarr; Coder &rarr; Skills as <span class="badge bg-secondary-lt">Managed</span> and not editable there.`,
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
					Desc:  `The project name above the file tree switches to another project's editor.`,
				},
				{
					Title: "The file tree",
					Desc:  `The folder button <i class="ti ti-folder align-text-bottom" aria-hidden="true"></i> is on every width: on a wide screen it folds the tree column away and back, on a small one it opens the drawer the tree sits in. On a wide screen you can also drag the divider next to the tree to resize it. The fold and the width stay on this device, open folders are remembered per project, and closing a folder folds everything inside it.`,
				},
				{
					Title: "Quick open",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "O"}}},
					Desc:  `Open any project file by name. Pressing bare Shift twice does the same.`,
				},
				{
					Title: "Switch project",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "P"}}},
					Desc:  `The project name above the file tree opens the list of projects, and the shortcut opens it with the search field ready: type to narrow it, the arrows move through what is left, Enter opens that project's editor and Escape closes. The order follows the sort of the projects page, your own project is marked, and a project created or deleted in the cockpit comes and goes here at once, on every open editor, without a reload.`,
				},
				{
					Title: "Go to definition and usages",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "B"}}, {Caps: []string{"Shift", "F12"}}},
					Desc:  `For PHP, Go, TypeScript and JavaScript files. Hold <kbd>Ctrl</kbd> (<kbd>Cmd</kbd> on a Mac) and click a symbol to jump to its definition, or to its usages when the cursor already sits on the declaration; <kbd>Ctrl</kbd>+<kbd>B</kbd> does the same for the symbol under the cursor, <kbd>Shift</kbd>+<kbd>F12</kbd> always lists the usages. On touch, a tap on a symbol raises a small <em>Look up</em> action next to it. Typing in the usages list narrows the rows, a click or tap jumps there. A target the project does not hold opens too: a dependency's sources, the Go standard library, a PHP stub, a TypeScript type declaration. Those open as a read only tab, marked with a lock, and cannot be saved. Looking up works in them like anywhere else, so a jump carries on into the next dependency and back into your own code. The project's language servers start when its editor opens. The PHP and Go ones index the whole project first: the statusbar shows how far they are, and a lookup waits for that index instead of answering from half of it. The TypeScript and JavaScript one has nothing to index up front and answers straight away, so nothing shows there at the start; work it announces later shows like theirs. <em>Reindex</em> in the editor's menu starts them over. A JavaScript or TypeScript project without a <code>jsconfig.json</code> or <code>tsconfig.json</code> of its own gets a default one from the cockpit, so a lookup sees the whole project instead of only the open file; adding your own replaces it. Under Settings, Editor, LSP each language picks how its server runs.`,
				},
				{
					Title: "Git marks",
					Desc:  `In a git repository a changed file carries a letter, on its tree row and on its tab: <span class="text-green">A</span> added, <span class="text-cyan">U</span> untracked, <span class="text-yellow">M</span> modified, <span class="text-red">D</span> deleted, <span class="text-azure">R</span> renamed, <span class="text-red">!</span> conflicted. A folder carries a dot for the most pressing change under it, the tooltip says how many lines came and went, and a change somebody else makes arrives on its own.`,
				},
				{
					Title: "Changed lines in the gutter",
					Desc:  `In a git repository the open file marks its changes against the last commit in the gutter, without a switch: a <span class="text-green">green</span> bar on a new line, a <span class="text-azure">blue</span> bar on a changed one, a grey tick where lines were deleted. The marks follow your typing without a save; they rest while a diff or a comparison is open, and a file the last commit does not hold yet shows none.`,
				},
				{
					Title: "Diff and blame a file",
					Desc:  `Right-click a file, on its tab or its tree row: <em>Show git diff</em> puts it next to the last commit, <em>Show git blame</em> writes the commit and the author next to every line. <em>Diff against revision</em> compares it with any branch, tag or commit instead: type to search, and the list offers the branches, the remote branches and the tags whose name matches plus the commits whose subject or hash does, each with its author and date; a name or hash typed past the list works too. <em>File history</em> lists the commits that touched the file, each one opening the diff against exactly that state, and picking another revision in an open diff switches it in place. All of it is per file, the diff and the blame come back after a reload. To put two files on disk side by side instead, pick <em>Select for compare</em> on one and <em>Compare with</em> on the other.`,
				},
				{
					Title: "Commit from the editor",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "K"}}},
					Desc:  `The commit button <i class="ti ti-git-commit align-text-bottom" aria-hidden="true"></i> above the file tree, or <em>Commit</em> in the git sheet, turns the tree into the list of changes, each with a checkbox, and nothing starts picked. Only the checked files go in, exactly as they are in the working copy; whatever a coder has staged for other files stays staged and untouched. Clicking a row shows its diff. Write the message below, <kbd>Ctrl</kbd>+<kbd>Enter</kbd> commits; unsaved picked files are saved first, and <em>Amend</em> rewords or extends the last commit instead; with nothing picked it rewrites only the message. The message, the picks and an amend in progress are saved with the project as you type and tick, so a reload, another browser or another device opens the panel where you left it, and a successful commit clears them everywhere. The list opens grouped by folder, folders first and files after them on every level: a subfolder nests under its parent, a chain of folders with nothing of its own reads as one row, and a folder's checkbox picks and drops everything below it. The folders button <i class="ti ti-folders align-text-bottom" aria-hidden="true"></i> switches to a flat list, each file with its full path under its name, and the choice stays on this device. Conflicted files cannot be picked. <em>Commit and push</em> behind the arrow next to the button pushes right after the commit, to wherever <code>git push</code> goes in this repository; a commit whose push is refused stands, with the refusal in the panel.`,
				},
				{
					Title: "Revert a file or a folder",
					Desc:  `Right-click a changed file or folder in the tree, a file's tab, or a row in the commit panel's list, and pick <em>Revert changes</em>: the path goes back to the last commit, staged edits included, one revert leaves it clean, and a deleted file comes back. A file the last commit does not hold, untracked or just added, cannot be restored, so reverting deletes it; the confirmation says that before anything runs, and on a folder it counts what will be restored and what will be deleted. Ignored files are no changes and stay. Open files follow: a reverted file reads the disk again, unsaved edits included because discarding them is what was asked, and a file the revert deleted closes its tab.`,
				},
				{
					Title: "Branch, push and pull",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "G"}}},
					Desc:  `In a git repository the branch stands in the statusbar, with arrows for how many commits it is ahead <span class="text-secondary">↑</span> and behind <span class="text-secondary">↓</span> its upstream. Tapping it, or <em>Git</em> in the editor menu, opens the git sheet: <em>Switch branch</em> leads to the branch list with a Back on top, remote branches included, and checking one of those out creates the local branch that tracks it, and typing there searches the repository's branches rather than the visible list; <em>New branch</em> starts from the current state and normalizes what you type into a name git accepts, shown before anything is created; then commit, push, pull and fetch, and the recent commits, where a tap puts the open file next to that commit and the copy control carries the hash away. <em>Pull</em> only fast forwards, and <em>Force push</em> asks first and runs as force-with-lease, so work this copy has never fetched is not overwritten. There is no stash and no merge here on purpose: whatever git refuses is shown in git's own words and the working copy stays as it is. After a switch or a pull the tree, the marks and every saved tab follow the new state on their own; unsaved work is never touched. A remote that wants a passphrase or credentials asks you in a dialog that names the project and the action above ssh's or git's own question; it appears on every open cockpit page, survives a reload, and closes everywhere as soon as one device answers or cancels, so the phone can answer what the laptop started; the masked answer goes back and the action carries on; cancelling ends it with the reason in the message, and background refreshes never ask anything. While an action runs, the row you tapped carries a spinner, the statusbar shows one in the branch's place, the other rows wait, and a second tap starts nothing twice. In a project that is no repository yet the statusbar says so, and the sheet offers one action instead: clone a repository straight into the project folder, with whatever authentication git on the host can already do.`,
				},
				{
					Title:    "File and tab menus",
					Tag:      "Editor",
					TagClass: "bg-secondary-lt",
					Desc:     `Right-click a tab or a tree row for everything that acts on that one file: copy, download, extract, rename, revert, delete, and how you want to look at it, the preview, the git diff, the diff against a revision, the file history, the git blame and the two compare entries. On touch, long-press; tapping the already active tab also opens its menu. The editor menu <i class="ti ti-dots-vertical align-text-bottom" aria-hidden="true"></i> keeps only what acts on the editor as a whole.`,
				},
				{
					Title: "Reorder tabs",
					Desc:  `Drag editor tabs with the mouse to change their order; on touch the grip in <em>Open files</em> does it. Either way the order is stored per device.`,
				},
				{
					Title: "One menu for everything else",
					Desc:  `Next to the tabs the toolbar keeps only the menu <i class="ti ti-dots-vertical align-text-bottom" aria-hidden="true"></i> and, while the open file is unsaved, Save. Everything else is in that menu and reads the same on a phone and on a wide screen: the list of open files, go to file, find in the file and in the project, go to line, the editor settings, save all, the git sheet, and last the keyboard shortcuts, which open as a list to read. Fullscreen is the one entry a phone does not get, there is no window there to grow out of. What does not apply is not in the list, rather than in it and dead.`,
				},
				{
					Title: "The sheets take the keyboard",
					Desc:  `Every one of these views is a list of rows: the git sheet, the docker sheet, the open files and a file's history open with the first row marked, <kbd>↓</kbd> and <kbd>↑</kbd> move through the rows and wrap at both ends, scrolling the list as they go, and <kbd>Enter</kbd> runs the marked one. A row is marked the same way whether the keyboard walked to it or the mouse rests on it. <kbd>Escape</kbd> leaves a level at a time, out of the branch list or a container's addresses back to the sheet they were reached from, then out of the sheet. The branch and revision lists start in their filter field instead, where the arrows walk the results and the typing stays yours. A list that redraws itself keeps the mark where it stood, a container that starts or stops as much as a git action that disables the rows while it runs. On a wide screen the sheet takes three quarters of the editor and stands at its right edge, on a phone the full width from the bottom.`,
				},
				{
					Title: "The list of open files",
					Desc:  `<em>Open files</em> in the menu brings the open files up from the bottom, one row each with its folder under the name, the git letter in front and a dot when it is unsaved. Tapping a row goes to that file, the cross closes it, and the grip <i class="ti ti-grip-vertical align-text-bottom" aria-hidden="true"></i> drags it into another position. On touch that grip is the way to reorder; with a mouse the tabs themselves drag. The order is the same one either way and stays on this device.`,
				},
				{
					Title:    "Swipe to the next file",
					Tag:      "Touch",
					TagClass: "bg-blue-lt",
					Desc:     `Swipe left or right on the text to go to the next or the previous open file, in the order the list shows, and it wraps around at both ends like <kbd>Ctrl</kbd>+<kbd>Tab</kbd> does. A pill names the file you would land on, the same one the terminal swipe shows and in the same place. It works while <em>Wrap long lines</em> is on. With wrapping off the text itself scrolls sideways, and then the gesture belongs to the code. Scrolling up and down stays scrolling either way, and a fast release keeps it moving. While text is selected the swipe steps aside, so the selection keeps the gesture.`,
				},
				{
					Title: "Move files",
					Desc:  `Drag a file or a folder in the tree onto another folder to move it there, drop it on empty tree space to move it to the project root. A pill names the target folder while you drag, holding the pointer at the top or bottom edge scrolls the tree, and resting it on a closed folder opens that folder. Open tabs follow the new path, and a name that is already taken asks before it is replaced.`,
				},
				{
					Title: "Copy and paste files",
					Desc:  `The tree menu copies a file or a folder and pastes it into another folder; pasting into the folder it already sits in makes a numbered copy. The clipboard belongs to this browser, it is not shared with your other devices.`,
				},
				{
					Title: "Copy a path or a file's text",
					Desc:  `The tab menu and the tree menu of a file copy its path inside the project, and next to it its contents as text. What is unsaved in the editor is copied as it stands there, otherwise the file is read as it lies on disk. A file the editor does not open as text, an image or an archive, says so instead of copying anything.`,
				},
				{
					Title: "Upload files",
					Desc:  `Drop files or whole folders onto the file tree to upload them; dropping onto a folder puts them there, and a dropped folder keeps its structure. Pasting them into the editor does the same, into the folder that is selected in the tree. The tree context menu uploads files or a folder too, targeting the row's folder. A name that is already taken is listed before the upload starts, and replacing it needs one confirmation.`,
				},
				{
					Title: "Download a folder",
					Desc:  `The tree menu packs a folder into a <code>.tar.gz</code> and downloads it. Windows, macOS and Linux all unpack that with their built in tar.`,
				},
				{
					Title: "Extract an archive",
					Desc:  `A <code>.tar</code>, <code>.tar.gz</code> or <code>.zip</code> carries an extract entry in its tree menu, and opening it offers the same next to the download. It unpacks into a new folder beside the archive, so nothing existing is overwritten.`,
				},
				{
					Title: "A save never overwrites newer work",
					Desc:  `A file you opened is saved onto exactly that file. If a coder or git wrote it in the meantime, nothing is written and a dialog says so: <em>Reload</em> takes the version on disk into the editor and your unsaved changes are gone, <em>Cancel</em> keeps them and writes nothing. If the file was deleted instead, the dialog offers <em>Create again</em>, which writes what is in the editor as a new file, or <em>Cancel</em>. There is no way to force the save, and a file you created in the editor saves normally.`,
				},
				{
					Title: "Save",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "S"}}},
					Desc:  `Save the current file.`,
				},
				{
					Title: "Find in the file",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "F"}}},
					Desc:  `Open the find panel for the current file.`,
				},
				{
					Title: "Close the current file",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "X"}}},
					Desc:  `Closes the open tab, and an unsaved one asks first. The same shortcut closes a terminal on the attach pages.`,
				},
				{
					Title: "Find in files",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "F"}}},
					Desc:  `Search the contents of every project file and jump to a match.`,
				},
				{
					Title: "Preview files",
					Desc:  `<em>Show preview</em> <i class="ti ti-eye align-text-bottom" aria-hidden="true"></i> in a file's context menu puts a markdown or an SVG file next to its rendered form and follows what you type. It is per file and comes back after a reload. Images open in a viewer, video and audio in a player, everything else offers a download.`,
				},
				{
					Title: "Editor settings",
					Desc:  `<em>Editor settings</em> in the menu <i class="ti ti-dots-vertical align-text-bottom" aria-hidden="true"></i> sets tab width, indentation, font size, line wrapping and how a diff looks, side by side against inline, or automatic, which picks by the window width and follows it while a diff is open, and whether unchanged parts are folded. All of it stays on this device. A file covered by a project's .editorconfig takes its indentation from there, the control then only shows it.`,
				},
				{
					Title: "Fullscreen editor",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "Enter"}}},
					Desc:  `<em>Fullscreen</em> in the menu <i class="ti ti-dots-vertical align-text-bottom" aria-hidden="true"></i>, the shortcut, or a double-click on empty space in the tab strip. On a phone the entry is not there: the editor already has the screen.`,
				},
				{
					Title: "Terminal panel",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "J"}}},
					Desc:  `<em>Terminal</em> in the menu <i class="ti ti-dots-vertical align-text-bottom" aria-hidden="true"></i>, the terminal icon <i class="ti ti-terminal-2 align-text-bottom" aria-hidden="true"></i> in the statusbar, or the shortcut opens the project's coders and shells below the code. Tabs switch between them, <i class="ti ti-plus align-text-bottom" aria-hidden="true"></i> starts a new one or resumes a stopped coder, and the keys, the refresh and the file upload work like on the terminal pages. Open or closed is remembered per project. Desktop only.`,
				},
				{
					Title: "Docker view",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "D"}}},
					Desc:  `<em>Docker</em> in the menu <i class="ti ti-dots-vertical align-text-bottom" aria-hidden="true"></i>, the docker icon <i class="ti ti-brand-docker align-text-bottom" aria-hidden="true"></i> in the statusbar, which counts how many of the project's containers run, or the shortcut opens what the project's compose menu offers: its addresses, the stack logs, the compose commands and the output of the last run, and below them every container, side by side, as many per line as the width allows. On a desktop a container's shell and logs open in the terminal panel instead of taking the page.`,
				},
			},
		},
		{
			Key:   "docker",
			Title: "Docker",
			Icon:  "ti-brand-docker",
			Lead:  "Each project's compose containers and the commands that drive them.",
			Intro: `The cockpit keeps one connection to the Docker daemon and follows its event stream, so everything docker is live without anything polling. Containers belong to the project whose directory their compose file was started in. A machine without a reachable daemon simply shows none of this.`,
			Items: []DocsItem{
				{
					Title: "Container chips",
					Desc:  `Each compose container is a chip <i class="ti ti-brand-docker align-text-bottom" aria-hidden="true"></i> on its project's row, named by its compose service. Green means running, gray means stopped, red means a failing healthcheck. They stand in that order, wherever they are listed: what is unwell first, then what runs, then what does not, so the one container that wants attention is at the front and never behind a fold. The chips follow starts and stops live, also ones made from the command line.`,
				},
				{
					Title: "Container actions",
					Desc:  `A click or tap on the chip of a running container opens a shell inside it, the common reason to reach for one. A right-click or a long-press opens its menu instead: the published ports, an <em>Open</em> entry per address the container answers on, <em>Shell</em>, <em>Logs</em>, and <em>Start</em>, <em>Stop</em>, or <em>Restart</em>. A container that is not running has no shell, so there a click opens the menu. Logs are always a terminal, never a dialog: they keep talking, and the tab is one like any other. The logs icon <i class="ti ti-file-text align-text-bottom" aria-hidden="true"></i> on the chip opens that terminal with one tap.`,
				},
				{
					Title: "Where an Open entry goes",
					Desc:  `A container is reachable in two ways and both stand in the menu. A published port is opened on the address this page was reached on, at that port, <em>Open :18088</em>. A container behind a reverse proxy publishes nothing at all and is reached by host name, and that host stands in a label of the container, which is where the cockpit reads it: <em>Open app.example.com</em>, the routed addresses first, because where one exists it is the address you want. Such a link carries no port, the proxy answers on the default one, and it is opened over the same scheme this page uses, so the same setup works over http at home and over https behind an ingress.`,
				},
				{
					Title: "Which label carries an address",
					Desc:  `That is configuration, under Settings &rsaquo; Docker below the commands: one rule says which label to read, <code>*</code> standing for any part of it, and a regular expression with a <code>host</code> capture says where the address sits in its value; <code>path</code> and <code>port</code> may be captured too. Out of the box one rule covers the traefik router labels, which is what most setups write. A rule may pin <code>http</code> or <code>https</code> when the app is reached differently than the cockpit, and it may name a label that switches it off for a container, the way <code>traefik.enable=false</code> does. Each row says what is wrong with its pattern and what it finds in the containers running right now. Removing every rule is a real answer, only the published ports are then offered, and one button puts the default rule back.`,
				},
				{
					Title: "Compose actions",
					Desc:  `A project with a compose file carries a compose button <i class="ti ti-brand-docker align-text-bottom" aria-hidden="true"></i> next to its row actions, also while nothing runs yet. Its menu opens every address the project's containers answer on, so one is one click away without looking for its container first, follows the whole stack in one <em>Logs</em> terminal, and below that stands one entry per configured command, <em>Compose up</em>, <em>Compose down</em>, <em>Compose build</em> and a down that takes the volumes with it. Each one runs in the background; the chips follow along live, the docker icon rides a wave for as long as the command runs, on the project's row and in the editor's statusbar alike, and a notification tells when the run finished or failed; opening the run's output reads it away. The run does not belong to the page that started it: it keeps going when the cockpit restarts, and the notification still arrives afterwards.`,
				},
				{
					Title: "What a run wrote",
					Desc:  `The menu entry above the commands opens the output of the newest run of that stack, and so does the notification when it is over: the whole output, while it runs and afterwards, with the exit code at the top. <em>Cancel</em> ends a command that is still going, which works from any page and after a restart, because it goes at the process and not at the page that started it.`,
				},
				{
					Title: "Configuring the commands",
					Desc:  `Settings &rsaquo; Docker is where docker lives: the daemon to talk to, and below it the commands themselves. Each entry has an icon picked from a short list, a label, the command line, how long it may take, and whether it asks before it starts. The order there is the order in the menu, and the grip <i class="ti ti-grip-vertical align-text-bottom" aria-hidden="true"></i> on an entry drags it into a new place, with a finger too; the save stores the new order. A command runs in the stack's directory with <code>DOCKER_HOST</code> set, and it runs directly instead of through a shell, so quotes group words and nothing else is interpreted; the arguments it becomes stand under the field. A program written as <code>./deploy.sh</code> is looked for from the stack directory upwards to the project root, so one script can serve every stack below it. Removing every entry is a real answer, the menu then offers ports and logs alone and one button to put the defaults back.`,
				},
				{
					Title: "Deleting a project",
					Desc:  `A project whose containers the daemon still shows is emptied before its directory goes: every one of its stacks comes down with its volumes, named by the compose project the daemon reports, so nothing is left running or lying around without the project it belonged to. That one command is fixed and not one of the configured ones. It takes a moment: the row says <em>Deleting…</em> while it works, the rest of the page stays usable, and the row disappears when it is through. A restart in the middle of it does not leave the project half deleted, the row says <em>Deleting…</em> again and the deletion carries on. If a stack cannot be brought down, the deletion stops, its reason stands on the row, and nothing is removed.`,
				},
				{
					Title:    "Shell into a container",
					Tag:      "Terminal",
					TagClass: "bg-blue-lt",
					Desc:     `<em>Shell</em> opens a normal cockpit terminal that steps into the container, <em>Log terminal</em> one that follows its output. Both live in the tab strip and the quick nav like any shell, and when the container stops, the pane falls back to a plain shell in the project.`,
				},
				{
					Title: "Docker host",
					Desc:  `Which daemon the cockpit talks to is resolved automatically: <code>DOCKER_HOST</code>, then the current docker context, then the standard socket paths. A host set under Settings &rsaquo; Docker wins over all of that.`,
				},
			},
		},
		{
			Key:   "notifications",
			Title: "Notifications",
			Icon:  "ti-bell-ringing",
			Lead:  "When a coder or shell has news, and where it shows.",
			Intro: `A notification means that a coder or shell has news. Every one of them reads the same way: one line saying what happened, and below it what it happened in, the name in quotes with its project. Each target has at most one unread entry, and follow-up signals within 30 seconds are intentionally grouped. Opening a visible coder, shell, or split pane marks its news as read everywhere. Terminal icons double as status lights throughout the app: green means busy, blue means unread notifications, gray means idle.`,
			Items: []DocsItem{
				{
					Title: "Coders",
					Desc:  `Claude reports completed turns, questions, and permission requests through its injected hooks. Copilot emits a terminal bell.`,
				},
				{
					Title: "Shells",
					Desc:  `A command that runs for at least two seconds notifies when its prompt returns. Every shell starts with <code>PS0</code> and <code>PROMPT_COMMAND</code> set to see that, an rc file that overwrites them would turn the notices off. A bell always counts as news, so use <code>printf '\a'</code> when a script needs attention.`,
				},
				{
					Title:    "The assistant",
					Tag:      "Sparkles",
					TagClass: "bg-blue-lt",
					Desc:     `An answer shows its first words below the title, in the entry, in the toast and on the phone. A report about a job says how it ended, done, blocked, or expired, and names the job below that.`,
				},
				{
					Title: "In the browser",
					Desc:  `The bell <i class="ti ti-bell align-text-bottom" aria-hidden="true"></i>, the blue marks on terminals and projects, the browser title, a toast, and the selected jingle all represent the same unread notifications; the bell opens the list, where single entries or everything can be marked read. Sound needs one browser interaction first; its volume is stored per device.`,
				},
				{
					Title:    "A steered coder stays quiet",
					Tag:      "Ownership",
					TagClass: "bg-blue-lt",
					Desc:     `While the assistant steers a job on a coder, that coder's own news rings nowhere: its report is what reaches you. The entry is still listed, already read.`,
				},
				{
					Title: "A git question",
					Desc:  `A <code>dev-cockpit git</code> command waiting for a passphrase or credentials is news like any other, one entry per place, the project name when the directory lies in a project and its absolute path otherwise, so the question reaches you with no cockpit page open, phone included. Opening any page shows the dialog, and the entry marks itself read once the dialog stands in front of you or the question is answered, cancelled or run out anywhere. The editor's own git actions raise none of this on purpose: you started them on a page that already shows the dialog.`,
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
					Desc:  `The server waits two seconds, then checks whether the target is still unread. Notifications you are already viewing stay silent instead of sending duplicate browser, phone, or webhook alerts.`,
				},
				{
					Title: "Web push",
					Desc:  `Enable it on each device in Settings &rarr; Notifications. It needs HTTPS and can alert while the app is closed. On iPhone and iPad, install Dev Cockpit to the home screen first. A device marked <span class="badge bg-warning-lt">Old keys</span> must be enabled again from that device after push keys change.`,
				},
				{
					Title: "Webhooks",
					Desc:  `Each registered webhook receives JSON with <code>text</code>, <code>title</code>, <code>body</code>, and <code>url</code>; Slack incoming webhooks work directly. Set the public base URL so outbound webhook links point back to this cockpit.`,
				},
			},
		},
		{
			Key:      "settings",
			Title:    "Settings and data",
			Icon:     "ti-settings",
			Lead:     "Appearance, optional behaviors, and moving your setup between hosts.",
			LinkURL:  "/settings/general",
			LinkText: "Open settings",
			Items: []DocsItem{
				{
					Title: "Light and dark mode",
					Desc:  `Page, editor, and terminal follow the OS scheme, there is no manual switch. Coders may take up to two seconds to apply a theme change.`,
				},
				{
					Title:    "How the machine is doing",
					Tag:      "Header",
					TagClass: "bg-secondary-lt",
					Desc:     `The server button <i class="ti ti-server align-text-bottom" aria-hidden="true"></i> in the header, on a phone as well, opens CPU, RAM and disk, each as a percentage with the plain numbers below it. The icon turns yellow when one of them passes 80 percent and red from 95, so a quiet header means a quiet machine. The disk is the one the projects live on. On Linux the CPU value is the share of the cores actually at work since the reading before. On a Mac it is the load average against the core count, so there it can pass 100 percent when more work is queued than the machine can run at once; the line under the value says which of the two it is. The values refresh while a page is open, every 5 seconds. The Float button <i class="ti ti-app-window align-text-bottom" aria-hidden="true"></i> in the panel puts the three values into a small card that floats over the page: drag it to where you want it, it stays put across pages and reloads, keeps itself inside a shrinking window, shows through the fullscreen terminal and editor, steps aside the moment a menu or dialog opens, and closes with its cross. On a wide screen it draws a ring per value; on a phone it shrinks to three slim bars so it costs barely a line.`,
				},
				{
					Title:    "Restore terminals at startup",
					Tag:      "Setting",
					TagClass: "bg-secondary-lt",
					Desc:     `Off by default. When on, a host reboot brings the working set back: coders resume, shells reopen empty in their project, and the tab order is kept.`,
				},
				{
					Title:    "Separate history per shell",
					Tag:      "Setting",
					TagClass: "bg-secondary-lt",
					Desc:     `Off by default. Gives every newly started shell its own command history instead of sharing the login shell's file; the history survives a restore.`,
				},
				{
					Title:    "Git in the editor",
					Tag:      "Setting",
					TagClass: "bg-secondary-lt",
					Desc:     `Settings &rarr; Editor sets how often the server looks for a change, in seconds, and when a file is big enough to ask before it is diffed. How a diff looks is not here, that one is per device in the editor's own settings.`,
				},
				{
					Title:    "Coder instructions, agents and skills",
					Tag:      "Coder",
					TagClass: "bg-secondary-lt",
					Desc:     `Settings &rarr; Coder edits a coder's own files: its global instructions, its agents, and its skills. With more than one coder installed, the sidebar lists them and picks whose files you edit.`,
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
