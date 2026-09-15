package web

import (
	"net/http"
	"strconv"

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
