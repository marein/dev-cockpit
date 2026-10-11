package backup

import "path/filepath"

// buildSections is the registry: the one list that says what a backup can
// carry and where each piece lives on this host. Everything else in the export
// and the import reads it, so a feature that starts keeping state is only in a
// backup once it appears here.
//
// That makes this the place to weigh a new state file, a directory or a host
// file the cockpit manages: which section it belongs to, whether it is worth a
// section of its own, and what it only makes sense together with (Requires).
// Leaving it out is a decision like any other, but it has to be a decision,
// because nobody notices a missing file until they need it on the new host.
//
// The ids and the source names are a compatibility surface. They are the path
// inside the archive (data/<section id>/<source name>), and an archive written
// a year ago still has to import into whatever this list says today. So they
// only ever get added: renaming or reusing one silently drops what an old file
// carries. What a section points at may move freely, that is resolved against
// this list at import time; what it is called may not.
func buildSections(stateDir, projectsDir, home string) []Section {
	st := func(name string) string { return filepath.Join(stateDir, name) }
	hm := func(name string) string { return filepath.Join(home, name) }
	// The cost books stay out of the backup on purpose, the price cache in
	// cost/prices.json is rebuilt by the next refresh.
	return []Section{
		{ID: "settings", Group: "Cockpit", Label: "Settings",
			Description: "General settings, the configured compose actions, the notification jingle and the worktree post script.",
			Sources: []Source{
				{Name: "settings.json", Path: st("settings.json")},
				{Name: "worktree-post-script", Path: st("worktree-post-script")}}},
		{ID: "push", Group: "Cockpit", Label: "Push channels",
			Description: "Web push devices, webhooks, the VAPID keys and the base URL. Devices keep ringing without a new registration as long as Dev Cockpit keeps its address, under a new address every device must register again. The keys let anyone holding this file push to the devices, and an old installation left running rings the same phones twice.",
			Sources: []Source{
				{Name: "push-vapid.json", Path: st("push-vapid.json")},
				{Name: "push-channels.json", Path: st("push-channels.json")},
				{Name: "push-subscriptions.json", Path: st("push-subscriptions.json")}}},
		{ID: "terminals", Group: "Cockpit", Label: "Terminal restore",
			Description: "The restore snapshot. With the restore setting on, coders and shells come back on the next start, in their project directories. The per shell command histories and the terminals last looked at travel along.",
			Sources: []Source{
				{Name: "terminal-restore.json", Path: st("terminal-restore.json")},
				{Name: "shell-history", Path: st("shell-history")},
				// The terminals last looked at are session ids, and an id
				// without the snapshot that brings that session back names
				// nothing on the new host. So it travels here and not with
				// the projects.
				{Name: "recent-terminals.json", Path: st("recent-terminals.json")}},
			Requires: []string{"projects", "claude-sessions", "copilot-sessions"}},
		{ID: "assistant", Group: "Cockpit", Label: "Assistants",
			Description: "The shared memory and every assistant's thread with the jobs it steers, its workspace and the files a message carried. The generated instruction files stay out, the next turn writes them from the memory again.",
			Sources: []Source{
				{Name: "assistant.json", Path: st("assistant/assistant.json")},
				{Name: "memory", Path: st("assistant/memory")},
				// One directory per assistant, holding its transcript, its
				// jobs and its workspace, so what belongs together travels
				// together. CLAUDE.md, AGENTS.md and the cockpit wrapper at
				// the top of a workspace are written before every turn (see
				// internal/assistant/memory.go) and carry this host's absolute
				// paths and version, so they are noise; the same names deeper
				// in the workspace are the assistant's own files and travel.
				{Name: "instances", Path: st("assistant/instances"), Skip: []string{"*/workspace/CLAUDE.md", "*/workspace/AGENTS.md", "*/workspace/cockpit"}},
				// The assistant last looked at is what the area's address
				// opens, and the ids it names travel in the index above.
				{Name: "recent-assistants.json", Path: st("recent-assistants.json")},
				// The three names the last release wrote its archives with.
				// They are absent on export from this layout and land an old
				// archive in the layout the startup move reads, so a backup
				// taken before the assistants moved into instances still
				// imports. TODO(v2.0.0): drop them once such an archive can no
				// longer exist.
				{Name: "conversations", Path: st("assistant/conversations")},
				{Name: "jobs.json", Path: st("assistant/jobs.json")},
				{Name: "workspace", Path: st("assistant/workspace"), Skip: []string{"CLAUDE.md", "AGENTS.md"}}},
			Requires: []string{"projects", "claude-sessions", "copilot-sessions"}},

		{ID: "claude", Group: "Coders", Label: "Claude config",
			Description: "Instructions, agents, skills, settings and the login credentials.",
			Sources: []Source{
				{Name: "CLAUDE.md", Path: hm(".claude/CLAUDE.md")},
				{Name: "settings.json", Path: hm(".claude/settings.json")},
				{Name: "settings.local.json", Path: hm(".claude/settings.local.json")},
				{Name: "keybindings.json", Path: hm(".claude/keybindings.json")},
				{Name: "credentials.json", Path: hm(".claude/.credentials.json")},
				{Name: "statusline.sh", Path: hm(".claude/statusline.sh")},
				{Name: "agents", Path: hm(".claude/agents")},
				{Name: "skills", Path: hm(".claude/skills")},
				{Name: "commands", Path: hm(".claude/commands")},
				{Name: "hooks", Path: hm(".claude/hooks")}}},
		{ID: "claude-sessions", Group: "Coders", Label: "Claude sessions",
			Description: "Session transcripts and prompt history, and which sessions run through Ollama. Can be large.",
			Sources: []Source{
				{Name: "projects", Path: hm(".claude/projects")},
				{Name: "history.jsonl", Path: hm(".claude/history.jsonl")},
				{Name: "claude-ollama-sessions.json", Path: st("claude-ollama-sessions.json")}}},
		{ID: "copilot", Group: "Coders", Label: "Copilot config",
			Description: "Instructions, agents, skills, settings and the login.",
			Sources: []Source{
				{Name: "copilot-instructions.md", Path: hm(".copilot/copilot-instructions.md")},
				{Name: "settings.json", Path: hm(".copilot/settings.json")},
				{Name: "config.json", Path: hm(".copilot/config.json")},
				{Name: "agents", Path: hm(".copilot/agents")},
				{Name: "skills", Path: hm(".copilot/skills")}}},
		{ID: "copilot-sessions", Group: "Coders", Label: "Copilot sessions",
			Description: "Session state and history. Can be large.",
			Sources: []Source{
				{Name: "session-state", Path: hm(".copilot/session-state")},
				{Name: "session-store.db", Path: hm(".copilot/session-store.db")},
				{Name: "session-store.db-wal", Path: hm(".copilot/session-store.db-wal")},
				{Name: "session-store.db-shm", Path: hm(".copilot/session-store.db-shm")},
				{Name: "command-history-state.json", Path: hm(".copilot/command-history-state.json")}}},
		{ID: "ollama", Group: "Coders", Label: "Ollama",
			Description: "The launcher's config.json, what ollama launch writes for the coders it wraps.",
			Sources:     []Source{{Name: "config.json", Path: hm(".ollama/config.json")}}},

		{ID: "projects", Group: "Host", Label: "Projects",
			Description: "The complete projects directory, every working copy as it sits on disk. Imports into the current projects dir. Can be large. The recent projects order, the projects the editor was last opened on, the editor's line comments and the project tags with their colors travel along.",
			Sources: []Source{
				{Name: "projects", Path: projectsDir},
				{Name: "recent-projects.json", Path: st("recent-projects.json")},
				{Name: "recent-editor-projects.json", Path: st("recent-editor-projects.json")},
				{Name: "line-comments", Path: st("line-comments")},
				{Name: "project-tags.json", Path: st("project-tags.json")}}},
		{ID: "ssh", Group: "Host", Label: "SSH keys",
			Description: "The complete ~/.ssh, keys, config, known_hosts, authorized_keys. File modes are preserved.",
			Sources:     []Source{{Name: "ssh", Path: hm(".ssh")}}},
		{ID: "git", Group: "Host", Label: "Git config",
			Description: "Global identity, aliases, helpers and the global gitignore: ~/.gitconfig, ~/.config/git, ~/.gitignore.",
			Sources: []Source{
				{Name: "gitconfig", Path: hm(".gitconfig")},
				{Name: "config-git", Path: hm(".config/git")},
				{Name: "gitignore", Path: hm(".gitignore")}}},
		{ID: "git-clis", Group: "Host", Label: "gh and glab logins",
			Description: "GitHub and GitLab CLI configuration including their tokens.",
			Sources: []Source{
				{Name: "gh", Path: hm(".config/gh")},
				{Name: "glab-cli", Path: hm(".config/glab-cli")}}},
		{ID: "docker", Group: "Host", Label: "Docker login",
			Description: "~/.docker/config.json with the registry logins, plus the docker contexts. A login held by a credential helper sits outside this file and does not travel.",
			Sources: []Source{
				{Name: "config.json", Path: hm(".docker/config.json")},
				{Name: "contexts", Path: hm(".docker/contexts")}}},
		{ID: "dotfiles", Group: "Host", Label: "Dotfiles",
			Description: "Every dot file directly in the home directory, bashrc, profile, inputrc, vimrc and friends, plus ~/.config/tmux. Files other sections cover, like .gitconfig, travel with those sections instead.",
			Sources:     []Source{{Name: "config-tmux", Path: hm(".config/tmux")}}},
	}
}
