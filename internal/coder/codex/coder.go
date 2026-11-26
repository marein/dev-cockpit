package codex

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/settings"
	"github.com/marein/dev-cockpit/internal/terminal"
)

// codex (OpenAI Codex CLI, verified against 0.172.0) keeps everything under
// one home, $CODEX_HOME or ~/.codex: the login, config.toml, the global
// AGENTS.md, the skills, the custom agents and one rollout file per session
// below sessions/.

type Coder struct {
	tools        []string
	agents       coder.AgentRepository
	sessions     *sessionRepository
	skills       coder.SkillRepository
	instructions coder.GlobalInstructions
	runtime      coder.SessionRuntime
	controls     terminal.ControlMapper
	// modelCache is what `codex debug models` printed, cached, and models the
	// repository over it, see models.go.
	modelCache *coder.ModelList[string]
	models     coder.ModelRepository
	// runner is the conversation capability, set once the probe passed.
	runner         *runner
	assistantProbe *coder.CapabilityProbe
}

// New builds the codex coder. store is where the model repository keeps the
// names it was told to remember; nil keeps them in memory.
func New(store *settings.Store) *Coder {
	home := codexHome()
	sessions := newSessionRepository(home)
	c := &Coder{
		tools:        []string{"codex"},
		agents:       agentRepository{dir: filepath.Join(home, "agents")},
		sessions:     sessions,
		skills:       coder.NewStandardSkillRepository(filepath.Join(home, "skills")),
		instructions: coder.NewFileGlobalInstructions(filepath.Join(home, "AGENTS.md")),
		runtime:      runtime{sessions: sessions},
		controls:     terminal.DefaultControlMapper(),
		modelCache:   coder.NewModelList(modelListTTL, listModels),
	}
	c.models = coder.NewModelRepository(store, "codex", codexModelsNote, c.cliModels)
	c.assistantProbe = coder.NewCapabilityProbe(c.probeAssistant, 10*time.Second)
	return c
}

func (p *Coder) ID() string                                   { return "codex" }
func (p *Coder) RequiredTools() []string                      { return p.tools }
func (p *Coder) AgentRepository() coder.AgentRepository       { return p.agents }
func (p *Coder) SessionRepository() coder.SessionRepository   { return p.sessions }
func (p *Coder) SkillRepository() coder.SkillRepository       { return p.skills }
func (p *Coder) GlobalInstructions() coder.GlobalInstructions { return p.instructions }
func (p *Coder) SessionRuntime() coder.SessionRuntime         { return p.runtime }
func (p *Coder) ControlMapper() terminal.ControlMapper        { return p.controls }

// ActivityProfile: the rollout is a readable record that marks a turn's start,
// its end and its abort, so it is watched, and codex's bell brings the end
// and a question as news, see BellSignal. The screen stands in until the
// first message, before which codex writes no record at all. Interrupt keys
// stay off: Escape closes codex's own popups as well, and an abort writes
// itself into the rollout. The cap is the backstop for a turn whose codex
// died without a word.
func (p *Coder) ActivityProfile() coder.ActivityProfile {
	return coder.ActivityProfile{
		WatchRecord:        true,
		WatchScreen:        true,
		InterruptKeys:      false,
		OpenTurnCap:        30 * time.Minute,
		MovementStartGrace: 20 * time.Second,
	}
}

func codexHome() string {
	if dir := strings.TrimSpace(os.Getenv("CODEX_HOME")); dir != "" {
		return dir
	}
	home, err := filesystem.HomeDir()
	if err != nil {
		home = "/root"
	}
	return filepath.Join(home, ".codex")
}
