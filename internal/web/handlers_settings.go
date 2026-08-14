package web

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/docker"
	"github.com/local/dev-cockpit/internal/editorintelligence"
	"github.com/local/dev-cockpit/internal/eventbus"
	"github.com/local/dev-cockpit/internal/restore"
	"github.com/local/dev-cockpit/internal/settings"
	"github.com/local/dev-cockpit/internal/shell"
	"github.com/local/dev-cockpit/internal/web/render"
)

// jingleSettingKey holds the cross-device notification jingle selection in
// the settings store.
const jingleSettingKey = "notification-jingle"

const defaultJingle = "arpeggio"

var jingleOptions = []render.JingleOption{
	{ID: "arpeggio", Label: "Arpeggio"},
	{ID: "doorbell", Label: "Doorbell"},
	{ID: "starlight", Label: "Starlight"},
	{ID: "retro", Label: "Retro"},
	{ID: "calm", Label: "Calm"},
}

func validJingle(id string) bool {
	for _, option := range jingleOptions {
		if option.ID == id {
			return true
		}
	}
	return false
}

// selectedJingle returns the stored jingle, falling back to the default when
// nothing valid is stored yet.
func (s *Server) selectedJingle() string {
	if id := s.settings.Get(jingleSettingKey); validJingle(id) {
		return id
	}
	return defaultJingle
}

func (s *Server) handleSettings(c *gin.Context) {
	c.Redirect(http.StatusSeeOther, "/settings/general")
}

func (s *Server) handleSettingsGeneral(c *gin.Context) {
	c.HTML(http.StatusOK, "settings_general.gohtml", render.SettingsGeneralData{
		Page:           s.page(c, "Settings", "settings"),
		SettingsNav:    s.settingsNav("general"),
		RestoreEnabled: s.settings.Get(restore.SettingKey) == "on",
		HistoryEnabled: s.settings.Get(shell.HistorySettingKey) == "on",
	})
}

// handleSettingsDocker is docker's own settings page: the daemon to talk to and
// the commands its menus offer. Both describe the same thing, what this cockpit
// does with docker, so they sit together and not in the general drawer.
func (s *Server) handleSettingsDocker(c *gin.Context) {
	dockerState := s.docker.State()
	dockerStatus := "No Docker host answers right now."
	if dockerState.Available {
		noun := "containers"
		if len(dockerState.Containers) == 1 {
			noun = "container"
		}
		dockerStatus = fmt.Sprintf("Connected to %s (%d %s).", dockerState.Host, len(dockerState.Containers), noun)
	}
	c.HTML(http.StatusOK, "settings_docker.gohtml", render.SettingsDockerData{
		Page:            s.page(c, "Settings", "settings"),
		SettingsNav:     s.settingsNav("docker"),
		DockerHost:      s.settings.Get(docker.HostSettingKey),
		DockerStatus:    dockerStatus,
		DockerConnected: dockerState.Available,
		Actions:         render.DockerActionRows(s.composeActions()),
		Icons:           render.DockerIcons(),
		// The preview reads the same cache every page reads, so a rule says
		// what it finds in the containers this machine runs at this moment.
		LinkRules:   render.DockerLinkRuleRows(s.linkRules(), dockerState.Containers),
		LinkSchemes: docker.LinkSchemes,
	})
}

// handleSettingsDockerSave stores the host and the command list together, the
// one form the page has.
func (s *Server) handleSettingsDockerSave(c *gin.Context) {
	host := strings.TrimSpace(c.PostForm("docker_host"))
	if host != "" {
		if err := docker.ValidateHost(host); err != nil {
			s.redirectWithAnchoredFlash(c, "/settings/docker", "settings-docker", "",
				"The Docker host must be a unix:// or tcp:// address.")
			return
		}
	}
	actions, err := composeActionsFromForm(c)
	if err != nil {
		s.redirectWithAnchoredFlash(c, "/settings/docker", "settings-docker", "", err.Error())
		return
	}
	rules, err := linkRulesFromForm(c)
	if err != nil {
		s.redirectWithAnchoredFlash(c, "/settings/docker", "settings-docker", "", err.Error())
		return
	}
	s.settings.Set(docker.HostSettingKey, host)
	storeComposeActions(s.settings, actions)
	storeLinkRules(s.settings, rules)
	s.docker.Kick()
	s.bus.Publish(eventbus.Event{Type: "docker"})
	s.redirectWithAnchoredFlash(c, "/settings/docker", "settings-docker", "Settings saved.", "")
}

// handleSettingsGeneralSave stores the general settings. The page is one
// form saving every section at once; the older per-section form values keep
// answering so a tab loaded before the merge still saves what it carries.
// Values are explicit ("on"/"off") instead of deleting the key, so a later
// default flip cannot silently re-enable a switch somebody turned off.
func (s *Server) handleSettingsGeneralSave(c *gin.Context) {
	onOff := func(field string) string {
		if c.PostForm(field) == "on" {
			return "on"
		}
		return "off"
	}
	switch c.PostForm("form") {
	case "general":
		s.settings.Set(restore.SettingKey, onOff("restore"))
		s.settings.Set(shell.HistorySettingKey, onOff("history"))
		s.redirectWithAnchoredFlash(c, "/settings/general", "settings-general", "Settings saved.", "")
	case "shell-history":
		s.settings.Set(shell.HistorySettingKey, onOff("history"))
		s.redirectWithAnchoredFlash(c, "/settings/general", "settings-shell-history", "Settings saved.", "")
	default:
		s.settings.Set(restore.SettingKey, onOff("restore"))
		s.redirectWithAnchoredFlash(c, "/settings/general", "settings-terminal-restore", "Settings saved.", "")
	}
}

// storeComposeActions writes the edited list, unless it is exactly the default
// one: saving the defaults unchanged says nothing that the absent key does not
// already say, and storing them would freeze this install on today's list. So
// that case takes the key out instead, the same thing the restore button does.
func storeComposeActions(store *settings.Store, actions []docker.Action) {
	if docker.IsDefault(actions) {
		store.Delete(docker.ActionsSettingKey)
		return
	}
	store.Set(docker.ActionsSettingKey, docker.EncodeActions(actions))
}

// storeLinkRules writes the edited rules, and it is the same decision as the
// commands above: the defaults unchanged take the key out, so a later version
// may improve them, and an emptied list is stored as such, which leaves the
// menus with the published ports alone.
func storeLinkRules(store *settings.Store, rules []docker.LinkRule) {
	if docker.IsDefaultLinkRules(rules) {
		store.Delete(docker.LinkRulesSettingKey)
		return
	}
	store.Set(docker.LinkRulesSettingKey, docker.EncodeLinkRules(rules))
}

// linkRulesFromForm reads the link rules off the same form. A rule whose
// pattern cannot be compiled is refused here rather than stored and skipped
// later: the field is right in front of the person who typed it.
func linkRulesFromForm(c *gin.Context) ([]docker.LinkRule, error) {
	labels := c.PostFormArray("link_label")
	patterns := c.PostFormArray("link_pattern")
	schemes := c.PostFormArray("link_scheme")
	unless := c.PostFormArray("link_unless")
	out := []docker.LinkRule{}
	for i := range labels {
		rule := docker.LinkRule{
			Label:   strings.TrimSpace(labels[i]),
			Pattern: strings.TrimSpace(at(patterns, i)),
			Scheme:  strings.TrimSpace(at(schemes, i)),
			Unless:  strings.TrimSpace(at(unless, i)),
		}
		// A row somebody added and left alone is not a rule.
		if rule.Label == "" && rule.Pattern == "" {
			continue
		}
		if err := rule.Validate(); err != nil {
			return nil, fmt.Errorf("%s: %s.", ruleName(rule, i), err)
		}
		out = append(out, rule)
	}
	return out, nil
}

// ruleName is what a complaint calls the row it is about: a rule has no name
// of its own, the label it reads is what a person recognises it by.
func ruleName(rule docker.LinkRule, index int) string {
	if rule.Label != "" {
		return rule.Label
	}
	return fmt.Sprintf("Link rule %d", index+1)
}

// composeActionsFromForm reads the compose commands off the docker settings
// form, the empty list included: taking every row away is a real answer and
// the surfaces then offer no buttons at all. Putting the defaults back is not
// one of the things this saves, it is its own route (see
// handleDockerActionsRestore): it clears the setting instead of writing rows,
// and a submit button could not reach it anyway, pe.js builds its body from
// the form alone and drops what the button carries.
func composeActionsFromForm(c *gin.Context) ([]docker.Action, error) {
	ids := c.PostFormArray("action_id")
	icons := c.PostFormArray("action_icon")
	labels := c.PostFormArray("action_label")
	commands := c.PostFormArray("action_command")
	timeouts := c.PostFormArray("action_timeout")
	// The confirm rides in a hidden field per row, aligned with the other
	// columns by position: a checkbox posts nothing while unchecked, and a
	// value keyed on the id would follow the wrong row once a duplicate id is
	// renamed.
	confirms := c.PostFormArray("action_confirm")
	used := map[string]bool{}
	out := []docker.Action{}
	for i := range ids {
		action := docker.Action{
			ID: strings.TrimSpace(ids[i]),
			// The picker offers the vocabulary, so anything else is a hand
			// written request: it takes the neutral icon rather than a name
			// nothing can draw.
			Icon:    docker.NormalizeIcon(strings.TrimSpace(at(icons, i))),
			Label:   strings.TrimSpace(at(labels, i)),
			Command: strings.TrimSpace(at(commands, i)),
			Timeout: strings.TrimSpace(at(timeouts, i)),
			Confirm: at(confirms, i) == "1",
		}
		// A row somebody added and left alone is not an entry.
		if action.Label == "" && action.Command == "" {
			continue
		}
		if action.Label == "" {
			return nil, fmt.Errorf("Every compose action needs a label (%q has none).", action.Command)
		}
		argv, err := docker.SplitCommand(action.Command)
		if err != nil {
			return nil, fmt.Errorf("%s: %s.", action.Label, err)
		}
		if len(argv) == 0 {
			return nil, fmt.Errorf("%s: the command is empty.", action.Label)
		}
		if action.Timeout != "" {
			if d, err := time.ParseDuration(action.Timeout); err != nil || d <= 0 {
				return nil, fmt.Errorf("%s: the timeout must be a duration like 10m.", action.Label)
			}
		}
		if action.ID == "" || used[action.ID] {
			action.ID = freeActionID(action.Label, used)
		}
		used[action.ID] = true
		out = append(out, action)
	}
	return out, nil
}

// at reads one row's field, tolerating a form that carries fewer of them than
// it has rows.
func at(list []string, i int) string {
	if i < len(list) {
		return list[i]
	}
	return ""
}

// freeActionID names a new entry after its label, which is what makes the
// stored list readable, and counts up when that name is taken.
func freeActionID(label string, used map[string]bool) string {
	base := actionSlug(label)
	id := base
	for n := 2; used[id]; n++ {
		id = fmt.Sprintf("%s-%d", base, n)
	}
	return id
}

func actionSlug(label string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(label) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		return "action"
	}
	return slug
}

// editorSettingsPath is the editor settings page. It sits behind a tab so the
// page can grow more of them, and the bare /settings/editor leads here the way
// a coder's base path leads to its instructions.
const (
	// editorSettingsPath is where the bare /settings/editor and the settings
	// sidebar's Editor row lead: the leftmost tab, the way a coder's base path
	// leads to its first section.
	editorSettingsPath       = editorSearchSettingsPath
	editorSearchSettingsPath = "/settings/editor/search"
	editorGitSettingsPath    = "/settings/editor/git"
	editorLSPSettingsPath    = "/settings/editor/lsp"
)

func (s *Server) handleSettingsEditor(c *gin.Context) {
	c.Redirect(http.StatusSeeOther, editorSettingsPath)
}

func (s *Server) handleSettingsEditorGit(c *gin.Context) {
	set := s.editorSettings()
	c.HTML(http.StatusOK, "settings_editor_git.gohtml", render.SettingsEditorData{
		Page:           s.page(c, "Settings", "settings"),
		SettingsNav:    s.settingsNav("editor"),
		Section:        "git",
		GitPollSeconds: set.GitPollSeconds,
		DiffMaxLines:   set.DiffMaxLines,
		DiffMaxKiB:     set.DiffMaxKiB,
	})
}

func (s *Server) handleSettingsEditorSearch(c *gin.Context) {
	set := s.editorSettings()
	c.HTML(http.StatusOK, "settings_editor_search.gohtml", render.SettingsEditorData{
		Page:        s.page(c, "Settings", "settings"),
		SettingsNav: s.settingsNav("editor"),
		Section:     "search",
		Exclusions:  set.Exclusions.String(),
	})
}

// handleSettingsEditorLSP renders the LSP tab: one select per language,
// offering the automatic default, the server over Docker, and Off. A
// select on purpose: another way to run a server joins as one more
// option, nothing about the form changes shape.
// The select shows the stored choice, never what automatic resolves to
// right now: the choice is what the form edits.
func (s *Server) handleSettingsEditorLSP(c *gin.Context) {
	dockerOK := s.docker.State().Available
	profiles := make([]render.EditorLSPProfile, 0)
	for _, p := range editorintelligence.Profiles() {
		selected := lspChoice(s.settings.Get(editorLSPServerKey(p.ID)), p)
		profiles = append(profiles, render.EditorLSPProfile{
			ID:       p.ID,
			Label:    p.Label,
			Command:  strings.Join(p.Command, " "),
			Server:   p.Server,
			Selected: selected,
			DockerOK: dockerOK,
		})
	}
	c.HTML(http.StatusOK, "settings_editor_lsp.gohtml", render.SettingsEditorData{
		Page:        s.page(c, "Settings", "settings"),
		SettingsNav: s.settingsNav("editor"),
		Section:     "lsp",
		LSPProfiles: profiles,
	})
}

// handleSettingsEditorLSPSave stores each language's pick: the automatic
// default, the server's name with the Docker marker, or off. A value the
// select never offered keeps the current setting instead of writing
// something no option stands for.
func (s *Server) handleSettingsEditorLSPSave(c *gin.Context) {
	for _, p := range editorintelligence.Profiles() {
		value := c.PostForm("server_" + p.ID)
		if value != "" && lspChoice(value, p) == value {
			s.settings.Set(editorLSPServerKey(p.ID), value)
		}
	}
	s.redirectWithFlash(c, editorLSPSettingsPath, "Settings saved.", "")
}

// handleSettingsEditorSearchSave stores the folder exclusions. An empty box is a
// real answer, meaning search everything, so it is written rather than ignored.
// The quick open index notices that the list changed by itself and rebuilds, so
// nothing has to be invalidated here.
func (s *Server) handleSettingsEditorSearchSave(c *gin.Context) {
	s.storeExclusions(c.PostForm("exclusions"))
	s.redirectWithFlash(c, editorSearchSettingsPath, "Settings saved.", "")
}

// handleSettingsEditorGitSave stores the editor settings. They are one form, so
// they are written together; a number that is not a number keeps its current
// value instead of dropping to a default the person never chose.
func (s *Server) handleSettingsEditorGitSave(c *gin.Context) {
	s.storeInt(editorGitPollSecondsKey, c.PostForm("git_poll_seconds"), 0, 60)
	s.storeInt(editorDiffMaxLinesKey, c.PostForm("diff_max_lines"), 0, 500000)
	s.storeInt(editorDiffMaxKiBKey, c.PostForm("diff_max_kib"), 0, 16384)
	s.redirectWithFlash(c, editorGitSettingsPath, "Settings saved.", "")
}

func (s *Server) handleSettingsNotifications(c *gin.Context) {
	devices := make([]render.PushDevice, 0)
	staleDevices := false
	for _, sub := range s.pusher.WebPush.Devices() {
		stale := s.pusher.WebPush.Stale(sub)
		staleDevices = staleDevices || stale
		icon := "ti-device-desktop"
		if strings.Contains(sub.Label, "iPhone") || strings.Contains(sub.Label, "iPad") || strings.Contains(sub.Label, "Android") {
			icon = "ti-device-mobile"
		}
		devices = append(devices, render.PushDevice{
			ID:       sub.ID,
			Label:    sub.Label,
			Endpoint: sub.Endpoint,
			Added:    sub.CreatedAt.Format("2006-01-02"),
			Icon:     icon,
			Stale:    stale,
		})
	}
	webhooks := make([]render.PushWebhook, 0)
	for _, hook := range s.pusher.Webhooks.List() {
		webhooks = append(webhooks, render.PushWebhook{ID: hook.ID, URL: hook.URL})
	}
	c.HTML(http.StatusOK, "settings_notifications.gohtml", render.SettingsNotificationsData{
		Page:           s.page(c, "Settings", "settings"),
		SettingsNav:    s.settingsNav("notifications"),
		Jingles:        jingleOptions,
		Selected:       s.selectedJingle(),
		VAPIDPublicKey: s.pusher.WebPush.PublicKey(),
		Devices:        devices,
		StaleDevices:   staleDevices,
		Webhooks:       webhooks,
		BaseURL:        s.pusher.BaseURL(),
	})
}

// handleSettingsNotificationsSave dispatches the settings page forms on
// their hidden form field, so every form POSTs to the path that renders it.
// The empty value stays routed to the jingle form, it predates the marker.
func (s *Server) handleSettingsNotificationsSave(c *gin.Context) {
	switch c.PostForm("form") {
	case "base-url":
		s.saveBaseURL(c)
	case "webhook-add":
		s.addWebhook(c)
	case "webhook-remove":
		if s.pusher.Webhooks.Remove(c.PostForm("id")) {
			s.redirectWithAnchoredFlash(c, "/settings/notifications", "settings-webhooks", "Webhook removed.", "")
			return
		}
		s.redirectWithAnchoredFlash(c, "/settings/notifications", "settings-webhooks", "", "The webhook was already gone.")
	case "push-remove":
		if s.pusher.WebPush.RemoveDevice(c.PostForm("id")) {
			s.redirectWithAnchoredFlash(c, "/settings/notifications", "settings-webpush", "Device removed.", "")
			return
		}
		s.redirectWithAnchoredFlash(c, "/settings/notifications", "settings-webpush", "", "The device was already gone.")
	case "", "jingle":
		s.saveJingleSetting(c)
	default:
		s.redirectWithFlash(c, "/settings/notifications", "", "Unknown form.")
	}
}

func (s *Server) saveJingleSetting(c *gin.Context) {
	jingle := c.PostForm("jingle")
	if !validJingle(jingle) {
		s.redirectWithAnchoredFlash(c, "/settings/notifications", "settings-jingle", "", "Unknown jingle.")
		return
	}
	s.settings.Set(jingleSettingKey, jingle)
	s.redirectWithAnchoredFlash(c, "/settings/notifications", "settings-jingle", "Settings saved.", "")
}

func (s *Server) saveBaseURL(c *gin.Context) {
	base := strings.TrimSpace(c.PostForm("base_url"))
	if base != "" {
		u, ok := parseHTTPURL(base, false)
		if !ok || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
			s.redirectWithAnchoredFlash(c, "/settings/notifications", "settings-base-url", "", "The base URL must be a plain http(s) address without query or fragment.")
			return
		}
		base = strings.TrimRight(base, "/")
	}
	s.pusher.SetBaseURL(base)
	s.redirectWithAnchoredFlash(c, "/settings/notifications", "settings-base-url", "Settings saved.", "")
}

func (s *Server) addWebhook(c *gin.Context) {
	webhook := strings.TrimSpace(c.PostForm("url"))
	if _, ok := parseHTTPURL(webhook, false); !ok {
		s.redirectWithAnchoredFlash(c, "/settings/notifications", "settings-webhooks", "", "The webhook must be an http(s) URL.")
		return
	}
	if err := s.pusher.Webhooks.Add(webhook); err != nil {
		s.redirectWithAnchoredFlash(c, "/settings/notifications", "settings-webhooks", "", err.Error())
		return
	}
	s.redirectWithAnchoredFlash(c, "/settings/notifications", "settings-webhooks", "Webhook added.", "")
}
