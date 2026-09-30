package claude

import (
	"maps"
	"path/filepath"
	"strings"
	"sync"

	"github.com/marein/dev-cockpit/internal/ollama"
	"github.com/marein/dev-cockpit/internal/statefile"
)

const (
	ollamaSessionsFile = "claude-ollama-sessions.json"
	ollamaHostEnv      = "OLLAMA_HOST"
)

func ollamaPick(model string) (string, bool) {
	name, ok := strings.CutPrefix(strings.TrimSpace(model), ollama.Prefix)
	if !ok || name == "" {
		return "", false
	}
	return name, true
}

func ollamaLaunch(model string) []string {
	return []string{"launch", "claude", "--model", model, "--yes", "--"}
}

type ollamaSessions struct {
	path   string
	mu     sync.Mutex
	models map[string]string
}

func newOllamaSessions(stateDir string) *ollamaSessions {
	if stateDir == "" {
		return nil
	}
	s := &ollamaSessions{path: filepath.Join(stateDir, ollamaSessionsFile)}
	s.models = s.load()
	return s
}

func (s *ollamaSessions) load() map[string]string {
	models := map[string]string{}
	statefile.Load(s.path, &models)
	return models
}

func (s *ollamaSessions) remember(sessionID, model string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.models[sessionID] == model {
		return
	}
	models := maps.Clone(s.models)
	models[sessionID] = model
	statefile.Save(s.path, 0o644, models)
	s.models = s.load()
}

func (s *ollamaSessions) forget(sessionID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.models[sessionID]; !ok {
		return
	}
	models := maps.Clone(s.models)
	delete(models, sessionID)
	statefile.Save(s.path, 0o644, models)
	s.models = s.load()
}

func (s *ollamaSessions) modelOf(sessionID string) string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.models[sessionID]
}
