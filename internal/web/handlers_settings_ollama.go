package web

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/ollama"
	"github.com/marein/dev-cockpit/internal/web/render"
)

const settingsOllamaPath = "/settings/coders/ollama"

func (s *Server) ollamaSettingsPage(c *gin.Context) bool {
	if !ollama.Available() {
		s.handleNotFound(c)
		return false
	}
	return true
}

func (s *Server) ollamaSettingsWrite(c *gin.Context) bool {
	if !s.ollamaSettingsPage(c) {
		return false
	}
	if s.localCall(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Ollama is set up on the settings page, not from a command."})
		return false
	}
	return true
}

func (s *Server) handleSettingsOllama(c *gin.Context) {
	if !s.ollamaSettingsPage(c) {
		return
	}
	var models []render.OllamaModel
	for _, m := range s.ollama.Models() {
		models = append(models, render.OllamaModel{Name: m.Name, Source: m.Source, Added: m.Source == ollama.SourceAdded})
	}
	c.HTML(http.StatusOK, "settings_ollama.gohtml", render.SettingsOllamaData{
		Page:        s.page(c, "Settings", "settings"),
		SettingsNav: s.settingsNav("ollama"),
		Address:     s.ollama.Host(),
		Models:      models,
		MaxRunes:    assistant.MaxModelRunes,
	})
}

func (s *Server) handleSettingsOllamaSave(c *gin.Context) {
	if !s.ollamaSettingsWrite(c) {
		return
	}
	if err := s.ollama.SetHost(c.PostForm("host")); err != nil {
		s.redirectWithFlash(c, settingsOllamaPath, "", err.Error())
		return
	}
	s.redirectWithFlash(c, settingsOllamaPath, "Settings saved.", "")
}

func (s *Server) handleSettingsOllamaModelAdd(c *gin.Context) {
	if !s.ollamaSettingsWrite(c) {
		return
	}
	name, err := assistant.CleanModel(c.PostForm("name"))
	if err == nil {
		err = s.ollama.Add(name)
	}
	if err != nil {
		s.redirectWithFlash(c, settingsOllamaPath, "", err.Error())
		return
	}
	s.redirectWithFlash(c, settingsOllamaPath, name+" added.", "")
}

func (s *Server) handleSettingsOllamaModelDelete(c *gin.Context) {
	if !s.ollamaSettingsWrite(c) {
		return
	}
	name := strings.TrimSpace(c.PostForm("name"))
	if err := s.ollama.Delete(name); err != nil {
		s.redirectWithFlash(c, settingsOllamaPath, "", err.Error())
		return
	}
	s.redirectWithFlash(c, settingsOllamaPath, name+" removed.", "")
}
