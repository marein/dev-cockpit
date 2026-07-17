package editorintelligence

import (
	"path/filepath"
	"slices"
	"sync"

	"github.com/local/dev-cockpit/internal/statefile"
)

// Completion modes. Off disables everything, ModeLSP serves language server
// completion only, ModeLSPAI adds the AI ghost text.
const (
	ModeOff   = "off"
	ModeLSP   = "lsp"
	ModeLSPAI = "lsp-ai"
)

var validDebounces = []int{200, 300, 500}

// Settings are the non secret editor intelligence preferences, cross device
// in the state dir.
type Settings struct {
	Mode string `json:"mode"`
	// AutoAI lets the ghost text fire on its own after the debounce; off
	// means AI answers only explicit requests.
	AutoAI     bool `json:"autoAi"`
	DebounceMs int  `json:"debounceMs"`
	// DisabledProfiles lists profile ids switched off. Storing the off
	// list keeps newly added profiles enabled by default.
	DisabledProfiles []string       `json:"disabledProfiles"`
	Ollama           OllamaSettings `json:"ollama"`
}

// OllamaSettings configure the local AI provider. The endpoint is fixed,
// only the model is chosen.
type OllamaSettings struct {
	Enabled bool   `json:"enabled"`
	Model   string `json:"model"`
}

// ProfileEnabled reports whether the profile takes part in completion.
func (s Settings) ProfileEnabled(id string) bool {
	return !slices.Contains(s.DisabledProfiles, id)
}

// AIConfigured reports whether the AI layer is switched on end to end.
func (s Settings) AIConfigured() bool {
	return s.Mode == ModeLSPAI && s.Ollama.Enabled && s.Ollama.Model != ""
}

func defaultSettings() Settings {
	return Settings{Mode: ModeOff, AutoAI: true, DebounceMs: 300}
}

// normalize forces stored values back into the valid ranges.
func (s Settings) normalize() Settings {
	if s.Mode != ModeOff && s.Mode != ModeLSP && s.Mode != ModeLSPAI {
		s.Mode = ModeOff
	}
	if !slices.Contains(validDebounces, s.DebounceMs) {
		s.DebounceMs = 300
	}
	return s
}

// ConfigStore persists Settings through the shared statefile conventions.
type ConfigStore struct {
	path string
	mu   sync.Mutex
}

// NewConfigStore returns the store for <stateDir>/editor-settings.json.
func NewConfigStore(stateDir string) *ConfigStore {
	return &ConfigStore{path: filepath.Join(stateDir, "editor-settings.json")}
}

// Load reads the settings, applying defaults for missing values.
func (s *ConfigStore) Load() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	settings := defaultSettings()
	statefile.Load(s.path, &settings)
	return settings.normalize()
}

// Save writes the settings.
func (s *ConfigStore) Save(settings Settings) {
	s.mu.Lock()
	defer s.mu.Unlock()
	statefile.Save(s.path, 0o644, settings.normalize())
}

// SecretStore holds the provider token a future external AI provider needs.
// It is separate from every other settings file and written 0600, and it
// exposes no way to render a stored token back out to a page.
type SecretStore struct {
	path string
	mu   sync.Mutex
}

type secretState struct {
	ProviderToken string `json:"providerToken,omitempty"`
}

// NewSecretStore returns the store for <stateDir>/editor-secrets.json.
func NewSecretStore(stateDir string) *SecretStore {
	return &SecretStore{path: filepath.Join(stateDir, "editor-secrets.json")}
}

// HasToken reports whether a provider token is stored, without revealing it.
func (s *SecretStore) HasToken() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load().ProviderToken != ""
}

// Token hands the stored token to a provider adapter.
func (s *SecretStore) Token() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load().ProviderToken
}

// SetToken stores or clears the provider token.
func (s *SecretStore) SetToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.load()
	state.ProviderToken = token
	statefile.Save(s.path, 0o600, state)
}

func (s *SecretStore) load() secretState {
	var state secretState
	statefile.Load(s.path, &state)
	return state
}
