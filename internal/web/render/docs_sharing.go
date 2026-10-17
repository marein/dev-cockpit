package render

func docsSharingTopic() DocsTopic {
	return DocsTopic{
		Key:      "sharing",
		Title:    "Sharing",
		Icon:     "ti-share",
		Lead:     "Projects, read only, for somebody without a login.",
		LinkURL:  "/settings/sharing",
		LinkText: "Open sharing",
		Items: []DocsItem{
			{
				Title: "Make a link",
				Desc:  `Settings &rarr; Sharing makes a link to a project, with an optional name for whom it is. It expires after the minutes, hours or days picked, at most 30 days, and is shown once, <em>Copy</em> puts it on the clipboard. The link carries the address you opened the cockpit at. <em>Share</em> in a project's row menu opens Sharing with that project picked.`,
			},
			{
				Title: "One link, several projects",
				Desc:  `<em>Add project</em> puts another project on a standing link, the guest finds it without a new link, also on a page already open. <em>Remove</em> takes one off at once, the last one asks first and ends the link. <em>Save</em> changes the name and, with a number, the expiry counted from now. Every project shows what the link grants there, <span class="badge bg-secondary-lt">Read only</span>.`,
			},
			{
				Title: "What the guest sees",
				Desc:  `Each project's editor with every file, <code>.env</code> and ignored files included, search, git diff, history and blame, code navigation inside the project, the line comments and the project's path on this host through <em>absolute</em> on <em>Copy path</em>. <em>Switch project</em> moves between the link's projects in the same tab. Every coder and shell of those projects live, also those started later, with the copy view. The projects' containers with their state and logs. Nothing can be saved, typed, committed, started or stopped. A guest never resizes a terminal, changes its theme, reads your news or moves what you opened last.`,
			},
			{
				Title: "When a link ends",
				Desc:  `At its expiry, when it is revoked, and when its last project goes: removed from it, deleted in the cockpit or gone from disk. A deletion in the cockpit takes the project off every link as soon as it starts, even when the deletion then fails. A link that ended, and a project that is not on it, answer like a page that does not exist. Links are not part of a backup.`,
			},
			{
				Title:    "Only from the browser",
				Tag:      "Assistant",
				TagClass: "bg-secondary-lt",
				Desc:     `An assistant cannot make or change a link, the local API refuses it.`,
			},
		},
	}
}
