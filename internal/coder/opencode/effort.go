package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/clirun"
	"github.com/marein/dev-cockpit/internal/coder"
)

// opencode's levels are the variants of one model, so a level is checked
// against the model it runs on, here and nowhere else: by CheckEffort for a
// pick being saved or a session being started, and by the runner before a
// turn. opencode itself runs a variant its model does not have as none at
// all, without a word (1.19.0, `run --variant bogus` answers like a run
// without one), which is why the check is the cockpit's.

// errEffortNeedsModel is an effort without a model to belong to.
var errEffortNeedsModel = errors.New("OpenCode takes an effort only together with a model, its levels are the variants of one model.")

// errVariantsUnknown is a list that could not be read, so no level is known.
var errVariantsUnknown = errors.New("opencode models did not answer")

// CheckEffort implements coder.EffortKeeper per model.
func (p *Coder) CheckEffort(model, level string) error {
	return checkVariant(p.modelVariants, model, level)
}

// checkVariant answers whether model takes level as one of its variants, with
// a sentence that says what it takes otherwise.
func checkVariant(variants func(string) ([]string, bool, error), model, level string) error {
	if level == "" {
		return nil
	}
	if model == "" {
		return errEffortNeedsModel
	}
	if variants == nil {
		return fmt.Errorf("%w, so the levels of %s are unknown right now.", errVariantsUnknown, model)
	}
	levels, listed, err := variants(model)
	switch {
	case err != nil:
		return fmt.Errorf("%w, so the levels of %s are unknown right now.", errVariantsUnknown, model)
	case slices.Contains(levels, level):
		return nil
	case !listed:
		return fmt.Errorf("opencode models does not list %s, so its levels are unknown.", model)
	case len(levels) == 0:
		return fmt.Errorf("%s takes no effort level.", model)
	}
	return fmt.Errorf("%s takes the effort levels %s.", model, strings.Join(levels, ", "))
}

// tuiStateFile is the file in opencode's state directory the terminal keeps
// its recent and favorite models and the variant it last ran each model at
// in, read here and never written.
const tuiStateFile = "model.json"

// agentConfigTimeout bounds the one `opencode debug config` a start with a
// level runs; it boots opencode's runtime, under a second on 1.19.0.
const agentConfigTimeout = 30 * time.Second

// PrepareStart implements coder.SessionPreparer for a start with a level. The
// terminal takes no effort flag (1.19.0, only `opencode run` has --variant),
// and a session created with a variant does not move it either: the terminal
// sends with every prompt the variant it remembers for the model, out of
// tuiStateFile, and where it remembers none the server takes the variant of
// the agent the prompt runs as, when that agent names the model too. So the
// level travels in the agent's configuration, set for this one terminal's
// process only, see StartCommand, and nothing of opencode's own state is
// written: Default stays opencode's own level, the one it remembers. This
// names the agent explicitly, the session's default where none was picked:
// `default_agent` of the resolved configuration, else build, opencode's own
// rule. A level the terminal would not run at is refused here, which is the
// one place its start can say so: one where opencode remembers another level
// for that model, which the terminal sends instead. Measured on 1.19.0 in
// fresh directories: the first prompt ran at the agent's variant and the
// footer showed it, with a remembered low it ran at low. opencode itself
// remembers the level of every session it opens, so after the first prompt
// it remembers this one's, the way it does for a session started anywhere.
func (r runtime) PrepareStart(start coder.SessionStart) (coder.SessionStart, error) {
	level := strings.TrimSpace(start.Effort)
	if level == "" {
		return start, nil
	}
	model := strings.TrimSpace(start.Model)
	if remembered := r.remembered(model); remembered != "" && remembered != level {
		return start, fmt.Errorf("OpenCode remembers the level %s for %s and starts its terminal on it, so the effort %s would not apply. Start on Default, or change the level inside OpenCode.", remembered, model, level)
	}
	if start.AgentID == "" {
		if r.agentOf == nil {
			return start, errors.New("The agent the effort applies to could not be named.")
		}
		agent, err := r.agentOf(start.Workdir)
		if err != nil {
			return start, err
		}
		start.AgentID = agent
	}
	return start, nil
}

// remembered is the variant the terminal remembers for model and would send,
// empty where it remembers none, remembers default or one the model does not
// have, all of which the terminal sends as none (1.19.0). A file that cannot
// be read is read as none.
func (r runtime) remembered(model string) string {
	if r.tuiState == "" {
		return ""
	}
	data, err := os.ReadFile(r.tuiState)
	if err != nil {
		return ""
	}
	var state struct {
		Variant map[string]string `json:"variant"`
	}
	if json.Unmarshal(data, &state) != nil {
		return ""
	}
	remembered := state.Variant[model]
	if remembered == "" || remembered == "default" || r.variants == nil {
		return ""
	}
	if levels, _, err := r.variants(model); err != nil || !slices.Contains(levels, remembered) {
		return ""
	}
	return remembered
}

// effortConfig is what OPENCODE_CONFIG_CONTENT carries for a start with a
// level: the agent the session runs as on the picked model at that variant.
// opencode merges it over every other configuration, for this process only.
func effortConfig(start coder.SessionStart) string {
	agent := map[string]any{"model": strings.TrimSpace(start.Model), "variant": strings.TrimSpace(start.Effort)}
	out, err := json.Marshal(map[string]any{"agent": map[string]any{start.AgentID: agent}})
	if err != nil {
		return ""
	}
	return string(out)
}

// defaultAgent is the agent a terminal in workdir starts as without --agent:
// `default_agent` of the configuration opencode resolves there, else build,
// the rule opencode follows (1.19.0). A build that configuration switches off
// is refused, the agent opencode would take then is not one this can name.
func defaultAgent(workdir string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), agentConfigTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "opencode", "debug", "config")
	cmd.Dir = workdir
	cmd.WaitDelay = time.Second
	out := &cappedBuffer{max: modelListMaxOutput}
	cmd.Stdout = out
	if err := cmd.Run(); err != nil || out.overflow {
		return "", errAgentUnknown
	}
	return agentOfConfig(out.buf.Bytes())
}

var errAgentUnknown = errors.New("OpenCode's configuration could not be read, so the agent the effort applies to is unknown.")

// agentOfConfig reads the default agent out of what `opencode debug config`
// printed, see defaultAgent.
func agentOfConfig(data []byte) (string, error) {
	var config struct {
		DefaultAgent string `json:"default_agent"`
		Agent        map[string]struct {
			Disable bool `json:"disable"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return "", errAgentUnknown
	}
	if agent := strings.TrimSpace(config.DefaultAgent); agent != "" {
		return agent, nil
	}
	if config.Agent["build"].Disable {
		return "", errors.New("OpenCode's build agent is switched off and no default agent is set, pick the agent the effort applies to.")
	}
	return "build", nil
}

// effortPrefix is what a start with a level puts in front of opencode on its
// command line: the agent configuration in the environment of this process.
func effortPrefix(start coder.SessionStart) string {
	if strings.TrimSpace(start.Effort) == "" || start.AgentID == "" {
		return ""
	}
	return "env OPENCODE_CONFIG_CONTENT=" + clirun.ShellQuote(effortConfig(start)) + " "
}
