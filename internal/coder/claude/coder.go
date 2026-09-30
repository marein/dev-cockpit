package claude

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/ollama"
	"github.com/marein/dev-cockpit/internal/settings"
	"github.com/marein/dev-cockpit/internal/terminal"
)

// endOfOptions terminates claude's own flag parsing, so the operand behind it is
// taken as text even when it starts with a dash. claude's prompt is a positional
// argument, and a positional that looks like an option is parsed as one: the
// paste "-dxdebug.idekey=PHPSTORM" arrives as the short flag -d with a filter,
// the prompt is gone, and a print run ends with "Input must be provided", which
// the turn reports as a coder that stopped before it finished. Values behind a
// flag like --session-id, --resume, --name or --agent are safe, claude takes the
// next argument whatever it looks like.
const endOfOptions = "--"

type Coder struct {
	tools  []string
	agents coder.AgentRepository
	// sessions is held as its own type, not as the interface: a session's
	// transcript is also what says whether its turn is over, see activity.go.
	sessions     *sessionRepository
	skills       coder.SkillRepository
	instructions coder.GlobalInstructions
	runtime      coder.SessionRuntime
	controls     terminal.ControlMapper
	models       coder.ModelRepository
	launcher     *ollama.Client
	launched     *ollamaSessions
	// runner is probed on first use, see assistant.go. A CLI without the flags
	// a turn needs loses the conversations only, never its terminal.
	assistantProbe *coder.CapabilityProbe
	runner         *runner
}

// New builds the claude coder. notifyInbox is the directory the injected
// Stop/Notification hooks drop their event files into; empty disables the
// hook injection. store is where the model repository keeps the names it
// was told to remember; nil keeps them in memory.
func New(stateDir, notifyInbox string, store *settings.Store, launcher *ollama.Client) *Coder {
	home, err := filesystem.HomeDir()
	if err != nil {
		home = "/root"
	}
	stateRoot := filepath.Join(home, ".claude", "projects")
	launched := newOllamaSessions(stateDir)
	c := &Coder{
		tools:        []string{"claude"},
		agents:       coder.NewStandardAgentRepository(filepath.Join(home, ".claude", "agents"), ".md"),
		sessions:     &sessionRepository{stateRoot: stateRoot, ollama: launched},
		skills:       coder.NewStandardSkillRepository(filepath.Join(home, ".claude", "skills")),
		instructions: coder.NewFileGlobalInstructions(filepath.Join(home, ".claude", "CLAUDE.md")),
		controls:     controlMapper{base: terminal.DefaultControlMapper()},
		launcher:     launcher,
		launched:     launched,
	}
	c.runtime = runtime{notifyInbox: notifyInbox, launcher: launcher, sessions: launched}
	c.models = modelRepository{ModelRepository: coder.NewModelRepository(store, "claude", claudeModelsNote, c.cliModels), launcher: launcher}
	c.assistantProbe = coder.NewCapabilityProbe(c.probeAssistant, 10*time.Second)
	return c
}

// claudeModels are the names a claude session and a turn are offered: the
// aliases, each always the newest model of its family, which is what keeps
// this list current without a list command, claude has none. A full name
// works too and is typed, and the repository then remembers it.
var claudeModels = []string{"fable", "opus", "sonnet", "haiku"}

// claudeModelsNote is the line under every select over that list.
const claudeModelsNote = "Aliases, always the newest of each family."

const ollamaModelsNote = "ollama/ names run through Ollama, cloud models only."

// ModelRepository implements coder.ModelKeeper, the one list the New coder
// dialog and the assistant's selects both read.
func (p *Coder) ModelRepository() coder.ModelRepository { return p.models }

func (p *Coder) cliModels() []coder.Model {
	models := make([]coder.Model, 0, len(claudeModels))
	for _, name := range claudeModels {
		models = append(models, coder.Model{Name: name})
	}
	for _, m := range p.launcher.Models() {
		name, err := assistant.CleanModel(m.Name)
		if err != nil || name == "" {
			continue
		}
		models = append(models, coder.Model{Name: ollama.Prefix + name, Source: ollama.Executable})
	}
	return models
}

type modelRepository struct {
	coder.ModelRepository
	launcher *ollama.Client
}

func (r modelRepository) Note() string {
	if ollama.Available() {
		return claudeModelsNote + " " + ollamaModelsNote
	}
	return claudeModelsNote
}

func (r modelRepository) Add(raw string) error {
	name, err := assistant.CleanModel(raw)
	if err != nil {
		return err
	}
	if bare, ok := ollamaPick(name); ok {
		if bare, err = assistant.CleanModel(bare); err != nil {
			return err
		}
		return r.launcher.Add(bare)
	}
	return r.ModelRepository.Add(name)
}

func (r modelRepository) Delete(raw string) error {
	name, err := assistant.CleanModel(raw)
	if err != nil {
		return err
	}
	if _, ok := ollamaPick(name); ok {
		return fmt.Errorf("%s is managed on the Ollama settings page.", name)
	}
	return r.ModelRepository.Delete(name)
}

func (p *Coder) Launcher(model string) string {
	if _, ok := ollamaPick(model); ok {
		return ollama.Executable
	}
	return ""
}

func (p *Coder) CheckModel(name string) error {
	if p.Launcher(name) != "" {
		return p.launcher.Check()
	}
	return nil
}

func (p *Coder) SessionModel(sessionID string) string {
	if name := p.launched.modelOf(sessionID); name != "" {
		return ollama.Prefix + name
	}
	return ""
}

func (p *Coder) ID() string                                   { return "claude" }
func (p *Coder) RequiredTools() []string                      { return p.tools }
func (p *Coder) AgentRepository() coder.AgentRepository       { return p.agents }
func (p *Coder) SessionRepository() coder.SessionRepository   { return p.sessions }
func (p *Coder) SkillRepository() coder.SkillRepository       { return p.skills }
func (p *Coder) GlobalInstructions() coder.GlobalInstructions { return p.instructions }
func (p *Coder) SessionRuntime() coder.SessionRuntime         { return p.runtime }
func (p *Coder) ControlMapper() terminal.ControlMapper        { return p.controls }

// ActivityProfile: the transcript is a readable record, so it is watched.
// Interrupt keys count because an abort during plain streaming may write
// nothing at all, while the same keys inside a tool call are left to the
// record, which an abort there provably writes. The cap is the backstop for
// an abort that happened past the cockpit's hands.
func (p *Coder) ActivityProfile() coder.ActivityProfile {
	return coder.ActivityProfile{
		WatchRecord:        true,
		InterruptKeys:      true,
		OpenTurnCap:        30 * time.Minute,
		MovementStartGrace: 20 * time.Second,
	}
}
