package web

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/recent"
	"github.com/marein/dev-cockpit/plugin"
)

// answerCoder answers every turn with AIRPORT.
type answerCoder struct{}

func (answerCoder) Available() []assistant.CoderInfo {
	return []assistant.CoderInfo{{ID: "claude", Label: "Claude", Runner: answerRunner{}}}
}

type answerRunner struct{}

func (answerRunner) Command(assistant.TurnRequest) (assistant.Command, error) {
	return assistant.Command{Name: "sh", Args: []string{"-c", "echo AIRPORT; echo DONE"}}, nil
}

func (answerRunner) Parse(_ string, events chan<- assistant.Event) assistant.Parser {
	return &answerParser{events: events}
}
func (answerRunner) SessionExists(string) bool  { return false }
func (answerRunner) DeleteSession(string) error { return nil }

type answerParser struct {
	events chan<- assistant.Event
	done   bool
}

func (p *answerParser) Line(line []byte) error {
	switch text := strings.TrimSpace(string(line)); text {
	case "DONE":
		p.done = true
	case "":
	default:
		p.events <- assistant.Event{Kind: assistant.EventDelta, Text: text}
	}
	return nil
}

func (p *answerParser) Finish() error {
	if !p.done {
		return errors.New("The turn ended early.")
	}
	return nil
}

func (p *answerParser) Diagnose(err error, _ string) error { return err }

// A plugin starts an assistant through the path `assistant-new` takes: named
// as asked, its first message answered as if the user had sent it. The list
// carries what the cockpit knows of it, the last message reads who wrote
// what and when, and once deleted it is absent and its id answers
// ErrAssistantNotFound.
func TestAPluginStartsListsAndReadsAssistants(t *testing.T) {
	ctx := context.Background()
	stateDir := t.TempDir()
	conversations, _, err := assistant.New(stateDir, answerCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{cfg: config.Config{StateDir: stateDir}, assistants: conversations, assistantRecent: recent.NewCapped(filepath.Join(stateDir, "recent-assistants.json"), recentEntries)}
	facade := s.PluginAssistants()

	if _, err := facade.Create(ctx, plugin.AssistantSpec{Message: strings.Repeat("x", assistant.MaxPromptBytes+1)}); err == nil || len(conversations.List()) != 0 {
		t.Fatalf("a message too long made an assistant: %v", err)
	}
	if _, err := facade.Create(ctx, plugin.AssistantSpec{CoderID: "nobody"}); err == nil || len(conversations.List()) != 0 {
		t.Fatalf("an unknown coder made an assistant: %v", err)
	}

	made, err := facade.Create(ctx, plugin.AssistantSpec{Name: " Release notes ", Message: "Write the notes."})
	if err != nil || made.ID == "" || made.Name != "Release notes" || !made.Named || made.CoderID != "claude" || made.URL != "/assistants/"+made.ID || made.CreatedAt.IsZero() {
		t.Fatalf("Create() = %+v, %v", made, err)
	}
	var last plugin.AssistantMessage
	deadline := time.Now().Add(10 * time.Second)
	for last.State != plugin.AssistantMessageStateComplete {
		if time.Now().After(deadline) {
			t.Fatalf("the first message was never answered, the last message is %+v", last)
		}
		time.Sleep(20 * time.Millisecond)
		if last, err = facade.LastMessage(ctx, plugin.AssistantMessageQuery{AssistantID: made.ID}); err != nil {
			t.Fatal(err)
		}
	}
	if last.Role != plugin.AssistantMessageRoleAssistant || last.Text != "AIRPORT" || last.ID == "" || last.Time.IsZero() {
		t.Fatalf("LastMessage() = %+v", last)
	}

	quiet, err := facade.Create(ctx, plugin.AssistantSpec{Name: "Quiet"})
	if err != nil || quiet.Running || quiet.MessageCount != 0 {
		t.Fatalf("an assistant without a message: %+v, %v", quiet, err)
	}
	if empty, err := facade.LastMessage(ctx, plugin.AssistantMessageQuery{AssistantID: quiet.ID}); err != nil || !reflect.DeepEqual(empty, plugin.AssistantMessage{}) {
		t.Fatalf("an assistant without messages read %+v, %v", empty, err)
	}

	listed, err := facade.List(ctx, plugin.AssistantQuery{})
	list := listed.Assistants
	if err != nil || len(list) != 2 || list[0].ID != quiet.ID || list[1].ID != made.ID {
		t.Fatalf("List() = %+v, %v", list, err)
	}
	if row := list[1]; row.MessageCount != 2 || row.Preview != "AIRPORT" || row.Running || row.LastMessageAt.IsZero() {
		t.Fatalf("the answered assistant is listed as %+v", row)
	}
	if found, err := facade.List(ctx, plugin.AssistantQuery{Contains: "airport"}); err != nil || len(found.Assistants) != 1 || found.Assistants[0].ID != made.ID {
		t.Fatalf("List(airport) = %+v, %v", found, err)
	}

	if err := conversations.Delete(made.ID); err != nil {
		t.Fatal(err)
	}
	if listed, _ := facade.List(ctx, plugin.AssistantQuery{}); len(listed.Assistants) != 1 || listed.Assistants[0].ID != quiet.ID {
		t.Fatalf("a deleted assistant is still listed: %+v", listed)
	}
	for _, id := range []string{made.ID, "../escape", ""} {
		if _, err := facade.LastMessage(ctx, plugin.AssistantMessageQuery{AssistantID: id}); !errors.Is(err, plugin.ErrAssistantNotFound) {
			t.Fatalf("LastMessage(%q) = %v", id, err)
		}
	}
}

// The roles and states a plugin reads are the plugin package's constants,
// spelled as the assistants store them.
func TestPluginAssistantMessageRolesAndStatesAreTheStoredOnes(t *testing.T) {
	for stored, read := range map[string]string{
		string(assistant.RoleUser):         plugin.AssistantMessageRoleUser,
		string(assistant.RoleAssistant):    plugin.AssistantMessageRoleAssistant,
		string(assistant.RoleCockpit):      plugin.AssistantMessageRoleCockpit,
		string(assistant.StateComplete):    plugin.AssistantMessageStateComplete,
		string(assistant.StateStreaming):   plugin.AssistantMessageStateStreaming,
		string(assistant.StateQueued):      plugin.AssistantMessageStateQueued,
		string(assistant.StateCancelled):   plugin.AssistantMessageStateCancelled,
		string(assistant.StateFailed):      plugin.AssistantMessageStateFailed,
		string(assistant.StateInterrupted): plugin.AssistantMessageStateInterrupted,
	} {
		if stored != read {
			t.Errorf("the store spells %q, a plugin reads %q", stored, read)
		}
	}
}

// twoCoders offers claude first and copilot beside it, both answering AIRPORT.
type twoCoders struct{}

func (twoCoders) Available() []assistant.CoderInfo {
	return []assistant.CoderInfo{{ID: "claude", Label: "Claude", Runner: answerRunner{}}, {ID: "copilot", Label: "Copilot", Runner: answerRunner{}}}
}

// The coders a plugin lists are the ones the assistants page offers, under
// the name the page shows, and the one marked Default is the one a create
// without a coder runs on: the first on a fresh install, the coder of the
// assistant opened last once there is one. Every listed id creates.
func TestAPluginListsTheCodersAnAssistantTakes(t *testing.T) {
	ctx := context.Background()
	stateDir := t.TempDir()
	conversations, _, err := assistant.New(stateDir, twoCoders{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	opened := recent.NewCapped(filepath.Join(stateDir, "recent-assistants.json"), recentEntries)
	s := &Server{cfg: config.Config{StateDir: stateDir}, assistants: conversations, assistantRecent: opened}
	facade := s.PluginAssistants()
	defaultOf := func(want string) {
		t.Helper()
		listed, err := facade.Coders(ctx, plugin.AssistantCoderQuery{})
		if err != nil || len(listed.Coders) != 2 {
			t.Fatalf("Coders() = %+v, %v", listed, err)
		}
		for i, name := range []string{"Claude", "Copilot"} {
			co := listed.Coders[i]
			if co.ID != strings.ToLower(name) || co.Name != name || co.Default != (co.ID == want) {
				t.Fatalf("Coders() = %+v, want %s as the default", listed.Coders, want)
			}
		}
		made, err := facade.Create(ctx, plugin.AssistantSpec{})
		if err != nil || made.CoderID != want {
			t.Fatalf("a create without a coder runs on %q, %v, the list marks %s", made.CoderID, err, want)
		}
	}

	defaultOf("claude")
	picked, err := facade.Create(ctx, plugin.AssistantSpec{Name: "On copilot", CoderID: "copilot"})
	if err != nil || picked.CoderID != "copilot" {
		t.Fatalf("a listed coder made %+v, %v", picked, err)
	}
	opened.Touch(picked.ID)
	defaultOf("copilot")

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := facade.Coders(cancelled, plugin.AssistantCoderQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("an ended context listed the coders: %v", err)
	}
}
