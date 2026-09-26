package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/settings"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// assistantJobsSettingsPath is the jobs tab of the assistant settings.
const assistantJobsSettingsPath = "/settings/assistant/jobs"

// The assistant's install wide settings, edited on a tab of /settings/assistant
// and stored as flat values in the shared settings store, like the editor's.
const assistantChecksKey = "assistant-max-checks"

// assistantChecksRange is what the setting accepts. One is the floor because a
// zero would stop every check and a job nothing checks is the promise this
// feature exists to keep; the ceiling is a guard against a typo, not a
// judgement about the machine, because every check is a paid turn and twenty of
// them at once is already far past what a person watches.
const (
	assistantChecksMin = 1
	assistantChecksMax = 20
)

// ConcurrentChecks is how many checks may run at once across every assistant.
// It is read again before every check, so a change applies without a restart,
// and it is a plain function of the store because the watcher is wired in main,
// before a server exists. A store that is not there, which the handler tests
// build, answers with the default.
func ConcurrentChecks(store *settings.Store) int {
	if store == nil {
		return assistant.DefaultConcurrentChecks
	}
	value, err := strconv.Atoi(store.Get(assistantChecksKey))
	if err != nil {
		return assistant.DefaultConcurrentChecks
	}
	if value < assistantChecksMin {
		return assistantChecksMin
	}
	if value > assistantChecksMax {
		return assistantChecksMax
	}
	return value
}

func (s *Server) handleSettingsAssistantJobs(c *gin.Context) {
	c.HTML(http.StatusOK, "settings_assistant_jobs.gohtml", render.SettingsAssistantJobsData{
		Page:        s.page(c, "Settings", "settings"),
		SettingsNav: s.settingsNav("assistant"),
		Section:     "jobs",
		MaxChecks:   ConcurrentChecks(s.settings),
		Min:         assistantChecksMin,
		Max:         assistantChecksMax,
		Default:     assistant.DefaultConcurrentChecks,
	})
}

func (s *Server) handleSettingsAssistantJobsSave(c *gin.Context) {
	s.storeInt(assistantChecksKey, c.PostForm("max_checks"), assistantChecksMin, assistantChecksMax)
	s.redirectWithFlash(c, assistantJobsSettingsPath, "Settings saved.", "")
}

// assistantApprovalsSettingsPath is the approvals tab of the assistant
// settings: one switch per kind of approval, each saying for every assistant
// whether that kind of action waits for the user.
const assistantApprovalsSettingsPath = "/settings/assistant/approvals"

// An approval is one kind of action an assistant may only take once the user
// said yes. Each kind is one entry of approvalKinds and one key in the
// settings store, assistant-approval-<id>, on until it is switched off: the
// key is read against "off", so an install that never saved it asks, and
// switching it on again removes the key rather than storing a copy of the
// default. A new kind is one more entry, the tab and the save read the list.
type approvalKind struct {
	ID    string
	Label string
	Hint  string
}

// approvalComposeActions is whether a confirm compose action an assistant
// starts waits for the user's approval.
const approvalComposeActions = "compose-actions"

var approvalKinds = []approvalKind{{
	ID:    approvalComposeActions,
	Label: "Compose actions approval",
	Hint:  "Compose commands marked to ask first under Settings › Docker wait for your approval.",
}}

// approvalKey is where one kind is stored.
func approvalKey(id string) string { return "assistant-approval-" + id }

// ApprovalAsks is one kind's switch, read fresh on every action it guards. A
// store that is not there, which the handler tests build, asks.
func ApprovalAsks(store *settings.Store, id string) bool {
	return store == nil || store.Get(approvalKey(id)) != "off"
}

// setApprovalAsks stores one kind's switch.
func setApprovalAsks(store *settings.Store, id string, ask bool) {
	if store == nil {
		return
	}
	if ask {
		store.Delete(approvalKey(id))
		return
	}
	store.Set(approvalKey(id), "off")
}

func (s *Server) handleSettingsAssistantApprovals(c *gin.Context) {
	data := render.SettingsAssistantApprovalsData{
		Page:        s.page(c, "Settings", "settings"),
		SettingsNav: s.settingsNav("assistant"),
		Section:     "approvals",
	}
	for _, kind := range approvalKinds {
		data.Approvals = append(data.Approvals, render.ApprovalRow{
			ID:    kind.ID,
			Label: kind.Label,
			Hint:  kind.Hint,
			Ask:   ApprovalAsks(s.settings, kind.ID),
		})
	}
	c.HTML(http.StatusOK, "settings_assistant_approvals.gohtml", data)
}

// handleSettingsAssistantApprovalsSave moves the switches the form carried.
// The hidden field in front of every switch is what makes an unticked box a
// posted value, and a kind the form did not post is left alone.
func (s *Server) handleSettingsAssistantApprovalsSave(c *gin.Context) {
	if s.localCall(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": approvalLocalRefusal})
		return
	}
	for _, kind := range approvalKinds {
		if values := c.PostFormArray("approval-" + kind.ID); len(values) > 0 {
			setApprovalAsks(s.settings, kind.ID, values[len(values)-1] == "1")
		}
	}
	s.redirectWithFlash(c, assistantApprovalsSettingsPath, "Settings saved.", "")
}

// approvalLocalRefusal is what a local call reads when it tries to answer an
// approval or to move an approval switch: both are the user's word, given in a
// browser.
const approvalLocalRefusal = "Approvals are the user's to give, in the browser. Ask the user."

// composeActionsLocalRefusal is what a local call reads when it tries to
// change the compose commands: which of them ask first is the approval's
// ground, so they are the user's to configure, in the browser.
const composeActionsLocalRefusal = "The compose commands are the user's to configure, in the browser. Ask the user."

// assistantTimezoneKey holds the zone a new schedule is read in when nobody
// named one for it. It is stored and not derived so that a schedule keeps
// meaning what it said: the name is resolved once, onto the trigger, and
// moving this afterwards moves nothing that already stands.
const assistantTimezoneKey = "assistant-timezone"

// AssistantTimezone is the zone a new schedule falls back to and whether
// somebody stored it. Two sources, in this order: what was stored, then the
// zone this server runs in. A caller naming one of its own stands in front of
// both, which is the surface's business, not this one's. The stored name is
// checked before it is handed out: a zone that does not load any more would
// refuse every schedule made after it, and the server's own zone always does.
func AssistantTimezone(store *settings.Store) (string, bool) {
	if store != nil {
		if name, ok := store.Lookup(assistantTimezoneKey); ok {
			if _, err := assistant.LoadZone(name); err == nil {
				return name, true
			}
		}
	}
	return assistant.ServerZone(), false
}

// assistantZone is that default, for the surface that fills a create in.
func (s *Server) assistantZone() string {
	name, _ := AssistantTimezone(s.settings)
	return name
}

// rememberAssistantZone moves the stored default, and only an explicit choice
// moves it: the zone somebody picked in the form, which they saw, and the
// `timezone-set` command, which says where the user sits. What a turn passes
// on one trigger is that trigger's zone and nothing more, because a turn reads
// a zone out of a sentence for the user, and a choice somebody derived must
// never become everybody's default.
func (s *Server) rememberAssistantZone(c *gin.Context, chosen string) {
	if s.settings == nil || s.localCall(c) {
		return
	}
	name := strings.TrimSpace(chosen)
	if name == "" {
		return
	}
	if _, err := assistant.LoadZone(name); err != nil {
		return
	}
	if stored, ok := s.settings.Lookup(assistantTimezoneKey); ok && stored == name {
		return
	}
	s.settings.Set(assistantTimezoneKey, name)
}
