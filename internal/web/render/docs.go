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
					Tag:      "Every page",
					TagClass: "bg-blue-lt",
					Desc:     `Mainly for phones, where it is the primary way to move around: the floating grid button <i class="ti ti-layout-grid align-text-bottom" aria-hidden="true"></i> browses active terminals and projects, and a project's list also starts a new coder or shell in that project. On the desktop the terminal switcher (double Ctrl) is the better route, because it leverages the keyboard.`,
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
					Desc:  `Tap Ctrl twice without another key in between. Type to filter; use the arrows to move; Enter opens the selection and Escape closes it. The palette includes active terminals, resumable coders, project editors, and new-terminal actions.`,
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
					Desc:     `Drop files onto a coder terminal to upload them, then reference them in a prompt by copying their path from the files dialog. The upload button <i class="ti ti-upload align-text-bottom" aria-hidden="true"></i> in the coder footer does the same.`,
				},
				{
					Title: "Refresh the stream",
					Desc:  `The refresh button <i class="ti ti-refresh align-text-bottom" aria-hidden="true"></i> in the footer reloads the terminal stream if the view looks out of sync.`,
				},
				{
					Title: "Font size, rows, and theme",
					Desc:  `The gear button <i class="ti ti-settings align-text-bottom" aria-hidden="true"></i> on a terminal page sets the font size, the visible rows, and the color theme, stored per device. Every palette follows the OS between a light and a dark variant.`,
				},
				{
					Title:    "Arrange tabs and panes",
					Tag:      "Desktop",
					TagClass: "bg-secondary-lt",
					Desc:     `Drag tabs to reorder them; the order is shared across devices. Hold a tab over the center of another tab briefly to make a split. Drag split pane headers to change their order.`,
				},
				{
					Title:    "Tab context menu",
					Tag:      "Desktop",
					TagClass: "bg-secondary-lt",
					Desc:     `Right-click a tab for rename, mark read, project and editor links, ungroup, stop, and delete. Split pane headers and the session chips on the projects page carry their own menu.`,
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
					Title: "Fullscreen terminal",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "F"}}, {Caps: []string{"Ctrl", "Shift", "Enter"}}},
					Desc:  `The fullscreen button <i class="ti ti-maximize align-text-bottom" aria-hidden="true"></i> sits in the terminal tab strip. Double-clicking unused space in that strip does the same thing.`,
				},
				{
					Title: "Choose the active split pane",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "ti-arrow-left"}}, {Caps: []string{"Ctrl", "Shift", "ti-arrow-right"}}},
					Desc:  `Moves focus between panes in a split terminal. The active pane receives keyboard input and shows its matching footer controls.`,
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
					Title: "The file tree",
					Desc:  `On desktop, drag the divider next to the tree to resize it. On small screens the tree sits in a drawer behind the folder button <i class="ti ti-folder align-text-bottom" aria-hidden="true"></i>.`,
				},
				{
					Title: "Editor settings",
					Desc:  `The settings menu <i class="ti ti-adjustments align-text-bottom" aria-hidden="true"></i> sets tab width, indentation, font size, and line wrapping, stored per device. A file covered by a project's .editorconfig takes its indentation from there, the control then only shows it.`,
				},
				{
					Title: "Switch projects",
					Desc:  `The project name above the file tree switches to another project's editor.`,
				},
				{
					Title: "Preview files",
					Desc:  `The eye button <i class="ti ti-eye align-text-bottom" aria-hidden="true"></i> previews markdown and SVG files; images open in a viewer.`,
				},
				{
					Title: "Upload files",
					Desc:  `Drop files onto the file tree to upload them; dropping onto a folder puts them there. The tree context menu uploads too, targeting the row's folder.`,
				},
				{
					Title:    "File and tab menus",
					Tag:      "Editor",
					TagClass: "bg-secondary-lt",
					Desc:     `Right-click a tab or a tree row for contextual actions. On touch, long-press; tapping the already active tab also opens its menu. The toolbar menu <i class="ti ti-dots-vertical align-text-bottom" aria-hidden="true"></i> collects what belongs to the open file: go to line, save all, copy path, download, rename, and delete.`,
				},
				{
					Title: "Reorder tabs",
					Desc:  `Drag editor tabs to change their order; the order is stored per device.`,
				},
				{
					Title: "Quick open",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "O"}}},
					Desc:  `Open any project file by name. Pressing bare Shift twice does the same.`,
				},
				{
					Title: "Find in the file",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "F"}}},
					Desc:  `Open the find panel for the current file.`,
				},
				{
					Title: "Find in files",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "F"}}},
					Desc:  `Search the contents of every project file and jump to a match.`,
				},
				{
					Title: "Save",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "S"}}},
					Desc:  `Save the current file.`,
				},
				{
					Title: "Cycle tabs",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Tab"}}, {Caps: []string{"Ctrl", "Shift", "Tab"}}},
					Desc:  `Step through the open editor tabs in strip order, wrapping at both ends.`,
				},
				{
					Title: "Fullscreen editor",
					Keys:  []DocsKeys{{Caps: []string{"Ctrl", "Shift", "Enter"}}},
					Desc:  `Use the toolbar button <i class="ti ti-maximize align-text-bottom" aria-hidden="true"></i>, the shortcut, or double-click empty space in the editor tab strip.`,
				},
			},
		},
		{
			Key:   "notifications",
			Title: "Notifications",
			Icon:  "ti-bell-ringing",
			Lead:  "When a coder or shell has news, and where it shows.",
			Intro: `A notification means that a coder or shell has news. Each target has at most one unread entry, and follow-up signals within 30 seconds are intentionally grouped. Opening a visible coder, shell, or split pane marks its news as read everywhere. Terminal icons double as status lights throughout the app: green means busy, blue means unread news, gray means idle.`,
			Items: []DocsItem{
				{
					Title: "Coders",
					Desc:  `Claude reports completed turns, questions, and permission requests through its injected hooks. Copilot emits a terminal bell.`,
				},
				{
					Title: "Shells",
					Desc:  `A command that runs for at least two seconds notifies when its prompt returns. A bell always counts as news, so use <code>printf '\a'</code> when a script needs attention.`,
				},
				{
					Title: "In the browser",
					Desc:  `The bell <i class="ti ti-bell align-text-bottom" aria-hidden="true"></i>, the blue marks on terminals and projects, the browser title, a toast, and the selected jingle all represent the same unread news; the bell opens the list, where single entries or everything can be marked read. Sound needs one browser interaction first; its volume is stored per device.`,
				},
			},
		},
		{
			Key:      "push",
			Title:    "Push delivery",
			Icon:     "ti-send",
			Lead:     "Get the same news when the page is closed.",
			LinkURL:  "/settings/notifications#settings-webpush",
			LinkText: "Open settings",
			Items: []DocsItem{
				{
					Title: "When delivery happens",
					Desc:  `The server waits two seconds, then checks whether the target is still unread. News you are already viewing stays silent instead of sending duplicate browser, phone, or webhook alerts.`,
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
					Title:    "Back up and move your setup",
					Tag:      "Data",
					TagClass: "bg-secondary-lt",
					Desc:     `Export the cockpit state to a file under Settings &rarr; Backup and import it on another host. Archives can be password-encrypted, and clashing files are shown for review before they overwrite.`,
				},
			},
		},
	}
}
