package render

func docsSharingTopic() DocsTopic {
	return DocsTopic{
		Key:      "sharing",
		Title:    "Sharing",
		Icon:     "ti-share",
		Lead:     "Open projects and assistants to somebody without a login, read only.",
		LinkURL:  "/settings/sharing",
		LinkText: "Open sharing",
		Items: []DocsItem{
			{
				Title: "Create a link",
				Desc:  `Settings &rarr; Sharing lists every link with its name, what it opens and its expiry. A project that allows <em>Comment</em> carries <i class="ti ti-message-circle align-text-bottom" aria-hidden="true"></i>, and an expiry less than a day away is marked. <em>New link</em> asks for a name, what the link <em>Opens</em>, picked from one searchable list of projects and assistants, and the expiry in minutes, hours or days, at most 30 days. Each pick stands below the list at once with its kind, its name and a cross that takes it off, a project with its own <em>Comment</em> switch. <em>Create link</em> shows the link once, <em>Copy</em> puts it on the clipboard. The link uses the address you opened the cockpit at. <em>Share</em> in a project's or an assistant's menu opens New link with it picked.`,
			},
			{
				Title: "Change a link",
				Desc:  `<em>Edit</em> on a link's row lists what it opens. Each project has a <em>Comment</em> switch, and each entry a cross that removes it. <em>Add</em> picks more from the same list, each listed with the rest at once, a project with its <em>Comment</em> switch. A number in the expiry counts from the save, an empty one keeps the expiry. <em>Save</em> applies everything at once. The guest gets what you added under the same link, also on a page already open. A link keeps at least one entry, <em>Revoke</em> ends it after asking. The name cannot be changed.`,
			},
			{
				Title: "What the guest sees",
				Desc:  `Each project's editor with every file, <code>.env</code> and ignored files included, search, git diff, history and blame, and code navigation, also into the sources of a dependency or a standard library as a read only tab. The line comments, each opened with <em>Show comment</em> in its line's menu. The project's path on this host through <em>absolute</em> on <em>Copy path</em>. <em>Switch project</em> moves between the link's projects in the same tab. Every coder and shell of these projects live, also those started later, with the copy view, the steered mark and the working ring. The projects' containers with their state and logs. On a wide screen, the status line with the server's CPU, RAM and disk, the addresses of the running stacks of the link's projects and the version. The guest saves, types, commits, starts and stops nothing, and comments only where the link allows it. A guest never resizes a terminal, changes its theme, reads your news or changes what you opened last.`,
			},
			{
				Title: "A guest's comments",
				Desc:  `On a project that allows <em>Comment</em>, the guest adds line comments through the line's menu, edits and deletes the ones written through their own link, and reads yours and those of other links. Each of their comments carries the link's name, for you and for every guest. You edit and delete every comment, a guest's comment keeps its name. Once <em>Comment</em> is taken away, the project leaves the link or the link ends, the guest's next write is refused. A page that is still open keeps its controls until a reload.`,
			},
			{
				Title: "An assistant on a link",
				Desc:  `The guest reads its whole conversation, the answer while it is written, under <em>Assistants</em> in the same tab as the link's projects. They open the files its messages link, nothing else of its workspace. Beside it stands its <em>Watching</em> as on your page, the coders it steers and the triggers it waits for, read only. The working ring shows as on your page: for a turn on its row, in the switcher and in the phone's head, with the tool at work in the answer, and for a check or a reaction on its row in <em>Watching</em>. Every running coder it steers or steered stands among the terminals, live with the copy view, unless another assistant steers it now. A stopped coder is gone. Links in the chat and the lists stay inside the link when it opens their target: a project's file or editor when the project is on it, a coder when the link shows it. Any other cockpit path in a message stays text. A link with a host stays as it is, on the cockpit's own host it leads to the login. Its memory, approvals, model picks and speech stay yours. The guest steers, releases, creates, changes and removes nothing, and starts no turn.`,
			},
			{
				Title: "When a link ends",
				Desc:  `At its expiry, when you revoke it, and when its last project or assistant is gone, deleted in the cockpit or removed from disk. A deletion in the cockpit takes the project or assistant off every link as soon as it starts, even if the deletion then fails. An ended link, and a project or assistant that is not on it, answer like a page that does not exist. Links are not part of a backup.`,
			},
			{
				Title:    "Only from the browser",
				Tag:      "Assistant",
				TagClass: "bg-secondary-lt",
				Desc:     `An assistant cannot create or change a link. The local API refuses it, so no assistant can share itself.`,
			},
		},
	}
}
