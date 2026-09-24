package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
	coderclaude "github.com/marein/dev-cockpit/internal/coder/claude"
	"github.com/marein/dev-cockpit/internal/settings"
)

// The seam between the settings store and a turn's command line, walked as
// runServe wires it and not through a fixture that skips it: a chat default
// stored under the Models tab's key reaches assistantCoders, which hands it
// to the assistant service as CoderInfo.Defaults, Service.Create copies it
// onto the new assistant's chat pick, and a chat turn sent through that
// service starts claude with `--model` and that name. An assistant made
// before the default was stored is copied nothing and keeps starting on the
// coder's start default, the live level the same wiring reads. The Checks
// and Trigger defaults stored beside the chat one are copied onto the new
// assistant the same way and reach neither chat turn. claude is a fake on
// PATH that passes the capability probe, writes its argv down and answers
// one finished turn, and HOME is a scratch directory, so the trust flag the
// turn writes lands nowhere near the one running this.
func TestAChatDefaultOnTheModelsTabReachesANewAssistantsArgv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	bin := t.TempDir()
	argv := filepath.Join(bin, "argv")
	fake := "#!/bin/sh\n" +
		"if [ \"$1\" = --help ]; then printf '%s\\n' '--print --output-format --include-partial-messages --verbose --session-id --resume --permission-mode'; exit 0; fi\n" +
		"printf '%s\\n' \"$@\" > " + argv + "\n" +
		"printf '%s\\n' '{\"type\":\"assistant\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"done\"}]}}'\n" +
		"printf '%s\\n' '{\"type\":\"result\",\"subtype\":\"success\",\"is_error\":false,\"result\":\"done\"}'\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(fake), 0o755); err != nil {
		t.Fatalf("fake claude: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	stateDir := t.TempDir()
	store := settings.New(filepath.Join(stateDir, "settings.json"))
	store.Set(assistant.ModelDefaultKey("claude", assistant.ModelPurposeStart), "fable")
	coders := assistantCoders{coders: []coder.Coder{coderclaude.New("", store)}, store: store}
	svc, _, err := assistant.New(stateDir, coders, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	before, err := svc.Create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	store.Set(assistant.ModelDefaultKey("claude", assistant.ModelPurposeChat), "haiku")
	store.Set(assistant.ModelDefaultKey("claude", assistant.ModelPurposeCheck), "sonnet")
	store.Set(assistant.ModelDefaultKey("claude", assistant.ModelPurposeTrigger), "opus")
	made, err := svc.Create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if made.Model != "haiku" || made.CheckModel != "sonnet" || made.TriggerModel != "opus" {
		t.Fatalf("want the three defaults copied onto the new assistant through the same wiring, got %+v", made.Summary)
	}
	if fresh, err := svc.Get(before.ID); err != nil || fresh.Model != "" || fresh.CheckModel != "" || fresh.TriggerModel != "" {
		t.Fatalf("want the assistant made before the defaults copied nothing, got %+v, %v", fresh.Summary, err)
	}

	// turn sends one prompt and reads the argv the fake wrote for it, then
	// waits the turn out: it is a real process writing into this test's
	// directories, and the next turn overwrites the same file.
	turn := func(id, want string) {
		t.Helper()
		_ = os.Remove(argv)
		if _, err := svc.Send(id, "hello", nil); err != nil {
			t.Fatalf("send: %v", err)
		}
		deadline := time.Now().Add(10 * time.Second)
		var seen []byte
		var err error
		for time.Now().Before(deadline) {
			if seen, err = os.ReadFile(argv); err == nil {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err != nil {
			t.Fatalf("the turn never started claude: %v", err)
		}
		args := strings.Split(strings.TrimSpace(string(seen)), "\n")
		at := -1
		for i, a := range args {
			if a == "--model" {
				at = i
			}
		}
		if at < 0 || at+1 >= len(args) || args[at+1] != want {
			t.Fatalf("want the chat turn started with --model %s out of the store, got argv %q", want, args)
		}
		if joined := strings.Join(args, "\n"); strings.Contains(joined, "sonnet") || strings.Contains(joined, "opus") {
			t.Fatalf("a Checks or Trigger default reached a chat turn: %q", args)
		}
		for time.Now().Before(deadline) {
			if fresh, err := svc.Get(id); err == nil && !fresh.Running {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("the turn never finished")
	}
	turn(made.ID, "haiku")
	turn(before.ID, "fable")
}
