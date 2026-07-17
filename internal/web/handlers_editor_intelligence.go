package web

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/editorintelligence"
	"github.com/local/dev-cockpit/internal/filesystem"
	"github.com/local/dev-cockpit/internal/web/render"
)

const maxIntelClientIDLength = 80

// editorCompletionRequest is the JSON body of a completion request. The
// content is the active unsaved buffer; position counts UTF-16 units like
// the CodeMirror document. Sources picks the requested layers, so the popup
// asks for lsp and the ghost text asks for ai without coupling their timing.
type editorCompletionRequest struct {
	Client   string `json:"client"`
	Path     string `json:"path"`
	Version  int    `json:"version"`
	Content  string `json:"content"`
	Position struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"position"`
	Trigger string   `json:"trigger"`
	Sources []string `json:"sources"`
}

type editorCloseRequest struct {
	Client string `json:"client"`
	Path   string `json:"path"`
}

// validIntelTarget checks the client id and resolves the path against the
// project root, writing the JSON error itself when invalid.
func (s *Server) validIntelTarget(c *gin.Context, root, client, path string) bool {
	if client == "" || len(client) > maxIntelClientIDLength {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A client id is required."})
		return false
	}
	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A file path is required."})
		return false
	}
	if _, err := filesystem.ResolveUnder(root, path); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return false
	}
	return true
}

// handleEditorCompletion answers one completion request for the active
// editor buffer. Unavailable sources answer with a status inside a 200, a
// malformed request is the only error case.
func (s *Server) handleEditorCompletion(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorCompletionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body."})
		return
	}
	if !s.validIntelTarget(c, p.Path, req.Client, req.Path) {
		return
	}
	if len(req.Content) > filesystem.MaxEditableBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The document is too large for completion."})
		return
	}
	sources := req.Sources
	if len(sources) == 0 {
		sources = []string{"lsp"}
	}
	resp, err := s.intel.Complete(c.Request.Context(), editorintelligence.Request{
		Client:      req.Client,
		ProjectName: p.Name,
		ProjectRoot: p.Path,
		Path:        req.Path,
		Version:     req.Version,
		Content:     req.Content,
		Line:        req.Position.Line,
		Character:   req.Position.Character,
		WantLSP:     slices.Contains(sources, "lsp"),
		WantAI:      slices.Contains(sources, "ai"),
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// handleEditorCompletionClose releases the document when a tab closes; the
// idle timeout would catch it anyway, this just frees it early.
func (s *Server) handleEditorCompletionClose(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorCloseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body."})
		return
	}
	if !s.validIntelTarget(c, p.Path, req.Client, req.Path) {
		return
	}
	s.intel.CloseDocument(req.Client, p.Name, req.Path)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// editorIntelConfig is the client configuration rendered into the editor
// page as a data attribute.
func (s *Server) editorIntelConfig() render.EditorIntelConfig {
	settings := s.intel.Config.Load()
	return render.EditorIntelConfig{
		Mode:       settings.Mode,
		AutoAI:     settings.AutoAI,
		DebounceMs: settings.DebounceMs,
		Exts:       editorintelligence.OwnedExtensions(),
	}
}

func (s *Server) handleSettingsEditor(c *gin.Context) {
	settings := s.intel.Config.Load()
	profiles := make([]render.EditorIntelProfile, 0, len(editorintelligence.Profiles()))
	for _, p := range editorintelligence.Profiles() {
		detection := p.Detect()
		profiles = append(profiles, render.EditorIntelProfile{
			ID:      p.ID,
			Label:   p.Label,
			Command: strings.Join(p.Command, " "),
			Path:    detection.Path,
			Found:   detection.Found,
			Enabled: settings.ProfileEnabled(p.ID),
		})
	}
	c.HTML(http.StatusOK, "settings_editor.gohtml", render.SettingsEditorData{
		Page:          s.page(c, "Settings", "settings"),
		Mode:          settings.Mode,
		AutoAI:        settings.AutoAI,
		DebounceMs:    settings.DebounceMs,
		Profiles:      profiles,
		OllamaEnabled: settings.Ollama.Enabled,
		OllamaModel:   settings.Ollama.Model,
		Connections:   s.intel.ConnectionCount(),
	})
}

// handleSettingsEditorSave dispatches the editor settings forms on their
// hidden form field, so every form POSTs to the path that renders it.
func (s *Server) handleSettingsEditorSave(c *gin.Context) {
	switch c.PostForm("form") {
	case "completion":
		s.saveEditorCompletionSettings(c)
	case "profiles":
		s.saveEditorProfileSettings(c)
	case "ai":
		s.saveEditorAISettings(c, false)
	case "ai-test":
		s.saveEditorAISettings(c, true)
	default:
		s.redirectWithFlash(c, "/settings/editor", "", "Unknown form.")
	}
}

func (s *Server) saveEditorCompletionSettings(c *gin.Context) {
	settings := s.intel.Config.Load()
	mode := c.PostForm("mode")
	if mode != editorintelligence.ModeOff && mode != editorintelligence.ModeLSP && mode != editorintelligence.ModeLSPAI {
		s.redirectWithAnchoredFlash(c, "/settings/editor", "settings-intel-completion", "", "Unknown completion mode.")
		return
	}
	settings.Mode = mode
	settings.AutoAI = c.PostForm("auto_ai") == "on"
	if debounce, err := strconv.Atoi(c.PostForm("debounce")); err == nil {
		settings.DebounceMs = debounce
	}
	s.intel.Config.Save(settings)
	s.redirectWithAnchoredFlash(c, "/settings/editor", "settings-intel-completion", "Settings saved.", "")
}

func (s *Server) saveEditorProfileSettings(c *gin.Context) {
	settings := s.intel.Config.Load()
	disabled := make([]string, 0)
	for _, p := range editorintelligence.Profiles() {
		if c.PostForm("profile_"+p.ID) != "on" {
			disabled = append(disabled, p.ID)
		}
	}
	settings.DisabledProfiles = disabled
	s.intel.Config.Save(settings)
	s.redirectWithAnchoredFlash(c, "/settings/editor", "settings-intel-profiles", "Settings saved.", "")
}

// saveEditorAISettings stores the AI section; with test set it also checks
// the provider afterwards, so the test always covers the just saved values.
// The check never sends source code.
func (s *Server) saveEditorAISettings(c *gin.Context, test bool) {
	settings := s.intel.Config.Load()
	model := strings.TrimSpace(c.PostForm("model"))
	if len(model) > 100 {
		s.redirectWithAnchoredFlash(c, "/settings/editor", "settings-intel-ai", "", "The model name is too long.")
		return
	}
	settings.Ollama.Enabled = c.PostForm("ollama_enabled") == "on"
	settings.Ollama.Model = model
	s.intel.Config.Save(settings)
	if !test {
		s.redirectWithAnchoredFlash(c, "/settings/editor", "settings-intel-ai", "Settings saved.", "")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if ok, reason := s.intel.TestProvider(ctx, model); !ok {
		s.redirectWithAnchoredFlash(c, "/settings/editor", "settings-intel-ai", "", reason)
		return
	}
	s.redirectWithAnchoredFlash(c, "/settings/editor", "settings-intel-ai", "Saved. Ollama is reachable and the model is ready.", "")
}
