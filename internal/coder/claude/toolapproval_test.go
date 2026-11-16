package claude

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/assistant"
)

// toolApprovalArgs are the turn's flags that point its permission prompts at
// the cockpit, each in the equals form, before claude's end of options, the
// last one where the Ollama launcher stands in front.
func toolApprovalArgs(t *testing.T, args []string) map[string]string {
	t.Helper()
	end := -1
	for i, arg := range args {
		if arg == endOfOptions {
			end = i
		}
	}
	got := map[string]string{}
	for i, arg := range args {
		for _, flag := range toolApprovalFlags {
			if value, ok := strings.CutPrefix(arg, flag+"="); ok {
				if end < 0 || i > end {
					t.Fatalf("want %s before the end of options, got %q", flag, args)
				}
				if _, twice := got[flag]; twice {
					t.Fatalf("want %s once, got %q", flag, args)
				}
				got[flag] = value
			}
		}
	}
	return got
}

// A turn of a claude that can ask hands its permission prompts to the tool
// approval server behind its own wrapper, unattended for a check or a
// reaction, and hides that tool from the model. The Ollama route carries the
// same flags behind the launcher.
func TestATurnAsksItsToolApprovalsThroughTheCockpit(t *testing.T) {
	withOllamaOnPath(t)
	r := &runner{sessions: stubSessions{}, launched: newOllamaSessions(t.TempDir()), asksTools: true}
	workdir := filepath.Join(t.TempDir(), "instances", "abc", "workspace")
	for _, tc := range []struct {
		req  assistant.TurnRequest
		args []string
	}{
		{assistant.TurnRequest{SessionID: sessionID, Workdir: workdir, Prompt: "hello"}, []string{"tool-approval-serve"}},
		{assistant.TurnRequest{SessionID: sessionID, Workdir: workdir, Prompt: "check", Unattended: true}, []string{"tool-approval-serve", "--unattended"}},
		{assistant.TurnRequest{SessionID: sessionID, Workdir: workdir, Prompt: "hello", Model: "ollama/qwen3.5:cloud"}, []string{"tool-approval-serve"}},
	} {
		cmd, err := r.Command(tc.req)
		if err != nil {
			t.Fatal(err)
		}
		got := toolApprovalArgs(t, cmd.Args)
		tool := "mcp__dev-cockpit__approve"
		if got["--permission-prompt-tool"] != tool || got["--disallowedTools"] != tool {
			t.Fatalf("want the prompt tool named and hidden, got %q", cmd.Args)
		}
		var config struct {
			MCPServers map[string]struct {
				Type    string   `json:"type"`
				Command string   `json:"command"`
				Args    []string `json:"args"`
			} `json:"mcpServers"`
		}
		if err := json.Unmarshal([]byte(got["--mcp-config"]), &config); err != nil {
			t.Fatalf("want the server configured inline, got %q: %v", got["--mcp-config"], err)
		}
		server, ok := config.MCPServers["dev-cockpit"]
		if !ok || len(config.MCPServers) != 1 || server.Type != "stdio" || server.Command != filepath.Join(workdir, "cockpit") || !slices.Equal(server.Args, tc.args) {
			t.Fatalf("want the wrapper serving it with %q, got %+v", tc.args, config)
		}
	}
}

// A claude without the flags runs its turns the way it always did.
func TestATurnOfAClaudeThatCannotAskAsksNothing(t *testing.T) {
	r := &runner{sessions: stubSessions{}}
	cmd, err := r.Command(assistant.TurnRequest{SessionID: sessionID, Workdir: "/work", Prompt: "hello", Unattended: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := toolApprovalArgs(t, cmd.Args); len(got) != 0 {
		t.Fatalf("want no tool approval flags, got %q", cmd.Args)
	}
}

// The flags are read off the help text as substrings: the prompt tool stands
// there only inside the text of --permission-prompts.
func TestTheToolApprovalFlagsAreReadOffTheHelp(t *testing.T) {
	help := `  --mcp-config <configs...>  Load MCP servers
  --disallowedTools, --disallowed-tools <tools...>
  --permission-prompts <target>  Who answers permission prompts with --print: "host" (the SDK host or
                                 --permission-prompt-tool) or "none"`
	if missing := missingFlags(help, toolApprovalFlags); len(missing) != 0 {
		t.Fatalf("want every flag found, missing %q", missing)
	}
	if missing := missingFlags("  --mcp-config <configs...>", toolApprovalFlags); !slices.Equal(missing, []string{"--permission-prompt-tool", "--disallowedTools"}) {
		t.Fatalf("want the two missing flags named, got %q", missing)
	}
}
