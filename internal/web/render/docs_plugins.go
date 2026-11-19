package render

func docsPluginsTopic() DocsTopic {
	return DocsTopic{
		Key:      "plugins",
		Title:    "Plugins",
		Icon:     "ti-puzzle",
		Lead:     "Add pages, buttons and routes with plugins that run beside the cockpit.",
		LinkURL:  "/settings/plugins",
		LinkText: "Open plugins",
		Items: []DocsItem{
			{
				Title: "Install a plugin",
				Desc:  `A plugin is a directory with a <code>plugin.json</code> and a program the cockpit starts. Settings &rarr; Plugins installs one from the absolute path of a directory on the host: the cockpit copies it into the plugins folder, without <code>.git</code>, and starts it. Installing from the same path again updates it. A directory dropped or linked into the folder shows up within two seconds.`,
			},
			{
				Title: "Install from a repository",
				Desc:  `<em>Install from a repository</em> takes the forge, GitHub or GitLab, and the address of a public repository on any https host, like <code>https://github.com/owner/name</code> or <code>https://git.example.com/group/sub/name</code>. <em>Look up</em> downloads the newest release with an archive for this host or for any, checks it against the release's checksums and reads its <code>plugin.json</code>. The dialog shows name, version, id, description, source and archive and installs only after you confirm. A release that does not fit, fails its checksum or holds a broken plugin is refused, nothing changes. Public repositories only, the cockpit sends no token.`,
			},
			{
				Title: "Updates",
				Desc:  `Plugins from a repository are checked a minute after the start and every six hours, <em>Check for updates</em> checks at once. A row with a newer release shows the step, like <code>1.2.0 → 1.3.0</code>, with the release notes, and <em>Update to 1.3.0</em> installs it. With <em>Auto updates on</em> newer releases install on their own. The previous release is kept: when the new one does not start or exits within 30 seconds, the cockpit rolls back and the row says why. A failed release is never installed automatically again. Updates come only from the forge, host and path a plugin was installed from. Installing a plugin whose id came from another source asks first. The plugin's state directory survives install, update and rollback.`,
			},
			{
				Title: "What a plugin can do",
				Desc:  `It runs as your user on the host, and its frontend code runs in the cockpit page, signed in as you. Install only plugins you trust. A plugin serves its own addresses below <code>/plugins/&lt;id&gt;/</code> behind your login, adds a page to the rail and the Cockpit sheet, buttons to the projects column and the empty projects page, settings and assistant memory, creates projects, starts assistants on a coder of its choice, lists them, reads their last message and follows jobs, coders and compose runs. An assistant a plugin starts with a first message answers it at once, a paid turn nobody approves.`,
			},
			{
				Title: "Where a plugin stands",
				Desc:  `Each row shows name, version, id, description and state. <em>Running</em> answers. <em>Starting</em> waits for the program to answer on its socket. <em>Crashed</em> shows why and the last output lines, the cockpit restarts it with a backoff of up to a minute. A plugin that stops answering is restarted. <em>Broken</em> shows what is wrong with its directory or <code>plugin.json</code>. <em>Disabled</em> is off. While a plugin is not running, its page says so, its buttons are gone and its addresses answer an error at once.`,
			},
			{
				Title: "Switch, restart, remove",
				Desc:  `<em>Disable</em> stops the plugin with all its processes and removes its entry and buttons from every open page. <em>Enable</em> starts it and brings them back, without a reload. <em>Restart</em> reads the directory again and starts the plugin, <em>Load again</em> does the same for a broken one. <em>Log</em> shows the last 200 output lines. <em>Remove</em> asks first, stops the plugin and deletes its directory with all kept releases, a linked directory only loses the link, its state directory stays. Plugins and their state are not part of a backup.`,
			},
			{
				Title: "Built in plugins",
				Desc:  `A build of the cockpit can carry its own plugins, listed under <em>Built in</em>. They update with the cockpit and cannot be removed. An always on plugin has no action. An optional one is off until <em>Enable</em>, <em>Disable</em> switches it off again, the switch survives restarts. Nothing of an optional plugin runs while it is off. Switched on, its page, buttons, settings and assistant memory appear on every open page without a reload. Switched off, its page and buttons go, the settings and memory it wrote stay. One that fails to start shows why and stays off.`,
			},
			{
				Title:    "Only from the browser",
				Tag:      "Assistant",
				TagClass: "bg-secondary-lt",
				Desc:     `An assistant cannot install, switch or remove a plugin, the local API refuses it.`,
			},
		},
	}
}
