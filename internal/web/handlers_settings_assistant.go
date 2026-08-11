package web

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/telegram"
	"github.com/local/dev-cockpit/internal/web/render"
)

// The assistant settings page is built from a list of sections, each with its
// own heading, its own forms and its own POST target. It carries one section
// today, Telegram; the next one is an entry in assistantSections plus its own
// partial, and nothing about the existing ones is touched.

// telegramSectionID is the section's id, its anchor and the last segment of
// its path.
const telegramSectionID = "telegram"

// assistantSectionPath is where a section posts to. GET on the same path
// renders the page, so the form path rule holds and a login redirect lands
// somewhere real.
func assistantSectionPath(id string) string { return "/settings/assistant/" + id }

// handleSettingsAssistant renders the assistant's settings. It also serves the
// GET of every section path, which is the page again: a section is a block of
// this page, not a page of its own.
func (s *Server) handleSettingsAssistant(c *gin.Context) {
	page := s.page(c, "Settings", "settings")
	c.HTML(http.StatusOK, "settings_assistant.gohtml", render.SettingsAssistantData{
		Page:        page,
		SettingsNav: s.settingsNav("assistant"),
		Sections:    s.assistantSections(page),
	})
}

// assistantSections is the page. Adding one here plus its partial is the whole
// cost of a new group of assistant settings.
func (s *Server) assistantSections(page render.Page) []render.SettingsSection {
	sections := []render.SettingsSection{{
		ID:        telegramSectionID,
		Title:     "Telegram",
		Lead:      "A second way into the assistant: write to a bot from your phone and it answers in the same conversation the browser shows. The connected chat talks to the assistant without logging in, so only one chat can be connected and only from here.",
		Template:  "settings_assistant_telegram",
		Action:    assistantSectionPath(telegramSectionID),
		Data:      s.telegramSettings(),
		CSRFToken: page.CSRFToken,
	}}
	for i := range sections {
		// The flash of the section that was just saved belongs into that
		// section's own block, next to the form it came from.
		if page.FlashProject == "settings-"+sections[i].ID && page.Flash.Message != "" {
			sections[i].Flash, sections[i].ShowFlash = page.Flash, true
		}
	}
	return sections
}

func (s *Server) telegramSettings() render.TelegramSettings {
	settings := s.telegram.Settings()
	status := s.telegram.Status()
	view := render.TelegramSettings{
		TokenSet:            settings.TokenSet,
		Enabled:             settings.Enabled,
		Status:              telegramStatusLine(settings, status),
		Stopped:             status.State == telegram.StateStopped,
		ChatName:            settings.ChatName,
		ChatID:              settings.ChatID,
		AnswersFromTelegram: settings.Answers == telegram.DeliveryTelegram,
		ReportsFromTelegram: settings.Reports == telegram.DeliveryTelegram,
	}
	if !settings.PairedAt.IsZero() {
		view.Paired = settings.PairedAt.Local().Format("2006-01-02 15:04")
	}
	if code, ok := s.telegram.Code(); ok {
		view.Code = code.Value
		view.CodeExpires = minutesLeft(code.Remaining(time.Now()))
	}
	return view
}

// telegramStatusLine says where the channel stands in one sentence. Without it
// a bot that says nothing is looked for on the phone.
func telegramStatusLine(settings telegram.Settings, status telegram.Status) string {
	switch {
	case status.State == telegram.StateStopped:
		return "The channel stopped: " + status.Reason + " Save a new token to start it again."
	case !settings.TokenSet:
		return "No bot token yet, so the channel is off."
	case !settings.Enabled:
		return "The channel is switched off. The bot token is kept."
	case settings.ChatID == 0:
		return "The channel is running and waits for a chat to connect."
	}
	return "The channel is running and listening for messages."
}

// minutesLeft rounds a remaining lifetime up, so a code with fifty seconds on
// it does not read as expired.
func minutesLeft(left time.Duration) string {
	minutes := int((left + time.Minute - time.Nanosecond) / time.Minute)
	if minutes <= 1 {
		return "1 minute"
	}
	return fmt.Sprintf("%d minutes", minutes)
}

// handleSettingsTelegramSave takes the Telegram section's forms, dispatched on
// their hidden form field. It answers only for its own section: another
// section's forms post to their own path.
func (s *Server) handleSettingsTelegramSave(c *gin.Context) {
	switch c.PostForm("form") {
	case "telegram-token":
		s.saveTelegramToken(c)
	case "telegram-token-clear":
		s.telegram.ClearToken()
		s.telegramFlash(c, "Bot token removed.", "")
	case "telegram-enabled":
		on := c.PostForm("enabled") == "on"
		s.telegram.SetEnabled(on)
		if on {
			s.telegramFlash(c, "Channel switched on.", "")
			return
		}
		s.telegramFlash(c, "Channel switched off.", "")
	case "telegram-delivery":
		s.telegram.SetDelivery(
			telegram.ParseDelivery(c.PostForm("answers")),
			telegram.ParseDelivery(c.PostForm("reports")),
		)
		s.telegramFlash(c, "Settings saved.", "")
	case "telegram-pair":
		code, err := s.telegram.NewCode()
		if err != nil {
			s.telegramFlash(c, "", err.Error())
			return
		}
		s.telegramFlash(c, "Send "+code.Value+" to the bot.", "")
	case "telegram-unpair":
		s.telegram.Unpair()
		s.telegramFlash(c, "Chat disconnected.", "")
	default:
		s.telegramFlash(c, "", "Unknown form.")
	}
}

// telegramFlash sends the answer back to the section's own block on the page.
func (s *Server) telegramFlash(c *gin.Context, message, errMsg string) {
	s.redirectWithAnchoredFlash(c, "/settings/assistant", "settings-"+telegramSectionID, message, errMsg)
}

// saveTelegramToken stores a pasted token. An empty field leaves the stored
// one alone, because the field never shows what is stored and submitting the
// form again would otherwise wipe it.
func (s *Server) saveTelegramToken(c *gin.Context) {
	if c.PostForm("token") == "" {
		s.telegramFlash(c, "Nothing changed, the stored bot token was kept.", "")
		return
	}
	if err := s.telegram.SetToken(c.PostForm("token")); err != nil {
		s.telegramFlash(c, "", err.Error())
		return
	}
	s.telegramFlash(c, "Bot token saved.", "")
}
