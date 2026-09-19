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
