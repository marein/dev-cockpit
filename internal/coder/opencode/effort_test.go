package opencode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
)

func entryNames(entries []modelEntry) []string {
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name)
	}
	return names
}

func listedVariants(entries ...modelEntry) func(string) ([]string, bool, error) {
	return func(model string) ([]string, bool, error) {
		for _, entry := range entries {
			if entry.Name == model {
				return entry.Variants, true, nil
			}
		}
		return nil, false, nil
	}
}

func unreadList(string) ([]string, bool, error) { return nil, false, errors.New("no answer") }

var effortModels = []modelEntry{
	{Name: "opencode/beam-free", Variants: []string{"low", "medium", "high", "xhigh", "max"}},
	{Name: "opencode/big-pickle"},
	{Name: "github-copilot/gpt-5.4", Variants: []string{"none", "low", "medium", "high", "xhigh"}},
	{Name: "opencode/muse", Variants: []string{"minimal", "low", "turbo"}},
}

// The levels a coder offers are every variant a listed model names, ordered
// the way the CLIs order theirs and a provider's own word behind them.
func TestTheLevelsAreEveryVariantOfTheList(t *testing.T) {
	list := newModelList(nil)
	list.cached, list.at = effortModels, time.Now()
	got := list.efforts(time.Now())
	want := []string{"none", "minimal", "low", "medium", "high", "xhigh", "max", "turbo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("want %v, got %v", want, got)
	}
}

// A check of a level waits for a list that never came back, and looks a
// model the cached list does not hold up in a fresh one, instead of refusing
// a level only because nobody had asked yet or the cache is behind; a list
// that cannot be read says so.
func TestTheVariantsOfAModelTheCacheLacksAreFetchedAndWaitedOn(t *testing.T) {
	var runs atomic.Int32
	list := newModelList(func(context.Context) ([]modelEntry, error) {
		runs.Add(1)
		return effortModels, nil
	})
	levels, listed, err := list.variants("opencode/beam-free")
	if err != nil || !listed || !reflect.DeepEqual(levels, effortModels[0].Variants) {
		t.Fatalf("want the model's variants out of the fetched list, got %v %v %v", levels, listed, err)
	}
	if _, listed, err := list.variants("opencode/no-such"); listed || err != nil {
		t.Fatalf("a model the list does not hold must read as not listed, got %v %v", listed, err)
	}
	if runs.Load() == 0 {
		t.Fatal("the empty list was never fetched")
	}

	stale := newModelList(func(context.Context) ([]modelEntry, error) { return effortModels, nil })
	stale.cached, stale.at = []modelEntry{{Name: "opencode/older-free"}}, time.Now()
	if levels, listed, _ := stale.variants("opencode/beam-free"); !listed || len(levels) != 5 {
		t.Fatalf("want a model the cache lacks found in a fresh list, got %v %v", levels, listed)
	}
	if entryNames(stale.entries(time.Now()))[0] != "opencode/beam-free" {
		t.Fatal("want the fresh list to replace the stale cache")
	}

	broken := newModelList(func(context.Context) ([]modelEntry, error) { return nil, errors.New("offline") })
	if _, _, err := broken.variants("opencode/beam-free"); err == nil {
		t.Fatal("want a list that cannot be read to say so")
	}
}

// The check says what the model takes, and never that it takes nothing while
// the list is unknown.
func TestCheckEffortSaysWhatTheModelTakes(t *testing.T) {
	variants := listedVariants(effortModels...)
	for _, c := range []struct {
		model, level, want string
	}{
		{"opencode/beam-free", "high", ""},
		{"opencode/beam-free", "", ""},
		{"", "high", "OpenCode takes an effort only together with a model, its levels are the variants of one model."},
		{"opencode/big-pickle", "high", "opencode/big-pickle takes no effort level."},
		{"github-copilot/gpt-5.4", "max", "github-copilot/gpt-5.4 takes the effort levels none, low, medium, high, xhigh."},
		{"opencode/typed", "high", "opencode models does not list opencode/typed, so its levels are unknown."},
	} {
		got := ""
		if err := checkVariant(variants, c.model, c.level); err != nil {
			got = err.Error()
		}
		if got != c.want {
			t.Fatalf("%s at %q: want %q, got %q", c.model, c.level, c.want, got)
		}
	}
	if err := checkVariant(unreadList, "opencode/beam-free", "high"); err == nil || err.Error() != "opencode models did not answer, so the levels of opencode/beam-free are unknown right now." {
		t.Fatalf("want an unread list named as such, got %v", err)
	}
}

// opencode runs a variant its model does not have as none at all, so a turn
// carries a level only for a model that names it, and is refused before a
// session exists otherwise; a list nobody could read is no refusal, the turn
// fails with that and a check tries again.
func TestATurnCarriesItsLevelBehindVariantOnlyForAModelThatTakesIt(t *testing.T) {
	r := testRunner(t)
	r.variants = listedVariants(effortModels...)
	cmd, err := r.Command(assistant.TurnRequest{SessionID: cockpitID, Resume: true, Workdir: t.TempDir(), Prompt: "hello", Model: "opencode/beam-free", Effort: "high"})
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	if argv := strings.Join(cmd.Args, " "); !strings.Contains(argv, " -m opencode/beam-free --variant high -- hello") {
		t.Fatalf("want the level behind --variant before the prompt, got %q", argv)
	}
	cmd, err = r.Command(assistant.TurnRequest{SessionID: cockpitID, Resume: true, Workdir: t.TempDir(), Prompt: "hello", Model: "opencode/beam-free"})
	if err != nil || strings.Contains(strings.Join(cmd.Args, " "), "--variant") {
		t.Fatalf("want no variant on a turn that names none, got %v %v", cmd.Args, err)
	}

	created := false
	r.create = func(string, string, string) (string, error) {
		created = true
		return "ses_fresh", nil
	}
	for _, c := range []struct {
		name, model, effort string
		kind                assistant.RefusalKind
	}{
		{"a level the model lacks", "opencode/beam-free", "none", assistant.RefusalUnknownEffort},
		{"a model without variants", "opencode/big-pickle", "high", assistant.RefusalUnknownEffort},
		{"a model the list does not hold", "opencode/typed", "high", assistant.RefusalUnknownEffort},
		{"no model at all", "", "high", assistant.RefusalEffortNeedsModel},
	} {
		_, err := r.Command(assistant.TurnRequest{SessionID: cockpitID, Workdir: t.TempDir(), Prompt: "hello", Model: c.model, Effort: c.effort})
		var refusal *assistant.Refusal
		if !errors.As(err, &refusal) || refusal.Kind != c.kind {
			t.Fatalf("%s: want a %s refusal, got %v", c.name, c.kind, err)
		}
	}
	r.variants = unreadList
	_, err = r.Command(assistant.TurnRequest{SessionID: cockpitID, Workdir: t.TempDir(), Prompt: "hello", Model: "opencode/beam-free", Effort: "high"})
	var refusal *assistant.Refusal
	if err == nil || errors.As(err, &refusal) || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("want an unread list as a plain failure, got %v", err)
	}
	if created {
		t.Fatal("a refused level created a session")
	}
}

// A start with a level hands it to the agent the session runs as, through the
// configuration of that one process, and writes nothing of opencode's own:
// the agent is the default one where none was picked.
func TestAStartHandsItsLevelToItsAgentAndWritesNothing(t *testing.T) {
	state := filepath.Join(t.TempDir(), "opencode", tuiStateFile)
	if err := os.MkdirAll(filepath.Dir(state), 0o755); err != nil {
		t.Fatal(err)
	}
	before := []byte(`{"recent":[],"favorite":[],"variant":{"github-copilot/gpt-5.4":"xhigh","opencode/beam-free":"default"}}`)
	if err := os.WriteFile(state, before, 0o600); err != nil {
		t.Fatal(err)
	}
	asked := 0
	r := runtime{variants: listedVariants(effortModels...), tuiState: state, agentOf: func(string) (string, error) {
		asked++
		return "build", nil
	}}
	start, err := r.PrepareStart(coder.SessionStart{Workdir: "/work", Model: "opencode/beam-free", Effort: "high"})
	if err != nil || start.AgentID != "build" || asked != 1 {
		t.Fatalf("want the default agent named, got %+v %v", start, err)
	}
	command := r.StartCommand(start)
	want := `exec env OPENCODE_CONFIG_CONTENT='{"agent":{"build":{"model":"opencode/beam-free","variant":"high"}}}' opencode --agent 'build' --model='opencode/beam-free'`
	if !strings.Contains(command, want) {
		t.Fatalf("want the level in the agent's configuration of this process, got %s", command)
	}
	if data, _ := os.ReadFile(state); string(data) != string(before) {
		t.Fatalf("the start wrote opencode's state: %s", data)
	}

	picked, err := r.PrepareStart(coder.SessionStart{Workdir: "/work", Model: "opencode/beam-free", Effort: "low", AgentID: "plan"})
	if err != nil || picked.AgentID != "plan" || asked != 1 || !strings.Contains(r.StartCommand(picked), `{"agent":{"plan":{"model":"opencode/beam-free","variant":"low"}}}`) {
		t.Fatalf("want a picked agent to carry the level, got %+v %v", picked, err)
	}
	plain, err := r.PrepareStart(coder.SessionStart{Workdir: "/work", Model: "opencode/beam-free"})
	if err != nil || plain.AgentID != "" || strings.Contains(r.StartCommand(plain), "OPENCODE_CONFIG_CONTENT") {
		t.Fatalf("want a start without a level left as it is, got %+v %v", plain, err)
	}
}

// opencode's terminal sends the level it remembers for a model over the
// agent's, so a start whose level it would not run at is refused, and one it
// remembers nothing usable for is not.
func TestAStartRefusesALevelTheTerminalWouldNotRunAt(t *testing.T) {
	state := filepath.Join(t.TempDir(), tuiStateFile)
	if err := os.WriteFile(state, []byte(`{"variant":{"opencode/beam-free":"low","opencode/muse":"turbo-ish","github-copilot/gpt-5.4":"default"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r := runtime{variants: listedVariants(effortModels...), tuiState: state, agentOf: func(string) (string, error) { return "build", nil }}
	_, err := r.PrepareStart(coder.SessionStart{Model: "opencode/beam-free", Effort: "high"})
	if err == nil || err.Error() != "OpenCode remembers the level low for opencode/beam-free and starts its terminal on it, so the effort high would not apply. Start on Default, or change the level inside OpenCode." {
		t.Fatalf("want the remembered level named, got %v", err)
	}
	for _, start := range []coder.SessionStart{
		{Model: "opencode/beam-free", Effort: "low"},
		{Model: "opencode/muse", Effort: "turbo"},
		{Model: "github-copilot/gpt-5.4", Effort: "high"},
	} {
		if _, err := r.PrepareStart(start); err != nil {
			t.Fatalf("%s at %s: want no refusal, got %v", start.Model, start.Effort, err)
		}
	}
}

func TestTheDefaultAgentIsOpencodesOwn(t *testing.T) {
	for _, c := range []struct{ config, want, refused string }{
		{`{"default_agent":"plan","agent":{}}`, "plan", ""},
		{`{"agent":{"myagent":{"prompt":"x"}}}`, "build", ""},
		{`{"agent":{"build":{"disable":true}}}`, "", "switched off"},
		{`not json`, "", "could not be read"},
	} {
		got, err := agentOfConfig([]byte(c.config))
		if got != c.want || (c.refused == "") != (err == nil) || (err != nil && !strings.Contains(err.Error(), c.refused)) {
			t.Fatalf("%s: want %q / %q, got %q %v", c.config, c.want, c.refused, got, err)
		}
	}
}
