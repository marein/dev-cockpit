package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/coder"
	toml "github.com/pelletier/go-toml/v2"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func agentIDs(agents []coder.Agent) string {
	ids := make([]string, len(agents))
	for i, a := range agents {
		ids[i] = a.ID
	}
	return strings.Join(ids, ",")
}

// The list holds what codex loads: a file with name, description and
// developer_instructions, also in a folder below agents/, by its name and
// not its file's name. A file missing one of the three, one that is no TOML,
// one of another suffix and a link are left out, and of two files naming the
// same agent the first counts.
func TestTheAgentsAreWhatCodexLoads(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pr-explorer.toml"), "name = \"pr_explorer\"\ndescription = \"Explores a PR.\"\ndeveloper_instructions = \"\"\"\nRead the diff.\n\"\"\"\nmodel = \"gpt-6-luna\"\n")
	writeFile(t, filepath.Join(dir, "team", "deep.toml"), "name = \"deep\"\ndescription = \"Nested.\"\ndeveloper_instructions = \"x\"\n")
	writeFile(t, filepath.Join(dir, "noname.toml"), "description = \"No name.\"\ndeveloper_instructions = \"x\"\n")
	writeFile(t, filepath.Join(dir, "nodesc.toml"), "name = \"nodesc\"\ndeveloper_instructions = \"x\"\n")
	writeFile(t, filepath.Join(dir, "blank.toml"), "name = \"blank\"\ndescription = \"Blank.\"\ndeveloper_instructions = \"  \"\n")
	writeFile(t, filepath.Join(dir, "number.toml"), "name = 7\ndescription = \"No string.\"\ndeveloper_instructions = \"x\"\n")
	writeFile(t, filepath.Join(dir, "broken.toml"), "name = \"broken\n")
	writeFile(t, filepath.Join(dir, "other.md"), "name = \"other\"\ndescription = \"Other suffix.\"\ndeveloper_instructions = \"x\"\n")
	writeFile(t, filepath.Join(dir, "twice.toml"), "name = \"deep\"\ndescription = \"Second.\"\ndeveloper_instructions = \"x\"\n")
	outside := filepath.Join(t.TempDir(), "outside.toml")
	writeFile(t, outside, "name = \"outside\"\ndescription = \"Behind a link.\"\ndeveloper_instructions = \"x\"\n")
	if err := os.Symlink(outside, filepath.Join(dir, "link.toml")); err != nil {
		t.Fatal(err)
	}

	r := agentRepository{dir: dir}
	agents := r.List()
	if got := agentIDs(agents); got != "deep,pr_explorer" {
		t.Fatalf("agents = %s", got)
	}
	explorer, err := r.Find("pr_explorer")
	if err != nil || explorer.Description != "Explores a PR." || explorer.Instructions != "Read the diff." || explorer.Path != filepath.Join(dir, "pr-explorer.toml") {
		t.Fatalf("explorer = %+v, %v", explorer, err)
	}
	if deep, _ := r.Find("deep"); deep.Description != "Nested." {
		t.Fatalf("want the first file in path order, got %+v", deep)
	}
	if _, err := r.Find("outside"); err == nil {
		t.Fatal("want a link left out")
	}
	if got := agentIDs((agentRepository{dir: filepath.Join(dir, "missing")}).List()); got != "" {
		t.Fatalf("want no agents without a folder, got %s", got)
	}
}

// A new agent is a file codex reads back as the same agent, its instructions
// a multi line string a person can read in the file, with the line breaks a
// text box posts as CRLF written as plain ones.
func TestASavedAgentIsReadByCodexAsWritten(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agents")
	r := agentRepository{dir: dir}
	res, err := r.Save("", "  Reviewer ", " Reviews a diff. ", "Review like an owner.\r\nDo not edit \"files\" \\ ever.\r\n")
	if err != nil || !res.Created || res.Saved.ID != "reviewer" {
		t.Fatalf("save = %+v, %v", res, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "reviewer.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := toml.Unmarshal(data, &fields); err != nil {
		t.Fatalf("want TOML, got %v:\n%s", err, data)
	}
	if fields["name"] != "reviewer" || fields["description"] != "Reviews a diff." || strings.TrimSpace(fields["developer_instructions"].(string)) != "Review like an owner.\nDo not edit \"files\" \\ ever." {
		t.Fatalf("fields = %#v", fields)
	}
	if !strings.Contains(string(data), "\"\"\"") || !strings.HasPrefix(string(data), "name = ") || strings.Contains(string(data), `\r`) {
		t.Fatalf("want the name first and the instructions as a multi line string without the form's CR:\n%s", data)
	}
	if _, err := r.Save("", "reviewer", "Again.", "x"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want a second agent of the name refused, got %v", err)
	}
	for _, tc := range []struct{ id, desc, instr, want string }{
		{"!!!", "d", "i", "letter or number"},
		{"a", " ", "i", "description is required"},
		{"a", "d", " \n ", "instructions are required"},
	} {
		if _, err := r.Save("", tc.id, tc.desc, tc.instr); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("save %+v = %v, want %q", tc, err, tc.want)
		}
	}
}

// An edit keeps the agent's file, its name where the identifier only spells
// it the way the cockpit's rule does, and every other setting the file holds.
// A rename moves it to <name>.toml in the agents folder and leaves no file of
// the old name behind.
func TestAnEditKeepsTheAgentsFileNameAndSettings(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "team", "explorer.toml")
	writeFile(t, nested, "# mine\nname = \"pr_explorer\"\ndescription = \"Old.\"\ndeveloper_instructions = \"Old.\"\nmodel = \"gpt-6-luna\"\nnickname_candidates = [\"Ada\", \"Bo\"]\n\n[mcp_servers.docs]\ncommand = \"docs-server\"\n")
	r := agentRepository{dir: dir}

	res, err := r.Save("pr_explorer", "pr-explorer", "New.", "New instructions.")
	if err != nil || res.Created || res.Saved.ID != "pr_explorer" || res.Saved.Path != nested {
		t.Fatalf("edit = %+v, %v", res, err)
	}
	data, _ := os.ReadFile(nested)
	var fields map[string]any
	if err := toml.Unmarshal(data, &fields); err != nil {
		t.Fatalf("want TOML, got %v:\n%s", err, data)
	}
	servers, _ := fields["mcp_servers"].(map[string]any)
	docs, _ := servers["docs"].(map[string]any)
	if fields["name"] != "pr_explorer" || fields["description"] != "New." || fields["model"] != "gpt-6-luna" || docs["command"] != "docs-server" || len(fields["nickname_candidates"].([]any)) != 2 {
		t.Fatalf("want the name and the other settings kept, got %#v", fields)
	}

	res, err = r.Save("pr_explorer", "scout", "Renamed.", "x")
	if err != nil || res.Saved.ID != "scout" || res.Saved.Path != filepath.Join(dir, "scout.toml") {
		t.Fatalf("rename = %+v, %v", res, err)
	}
	if _, err := os.Stat(nested); !os.IsNotExist(err) {
		t.Fatalf("want the old file gone, got %v", err)
	}
	if moved, _ := os.ReadFile(filepath.Join(dir, "scout.toml")); !strings.Contains(string(moved), "gpt-6-luna") {
		t.Fatalf("want the settings moved along:\n%s", moved)
	}
	if got := agentIDs(r.List()); got != "scout" {
		t.Fatalf("agents = %s", got)
	}
	if _, err := r.Save("nobody", "x", "d", "i"); err == nil {
		t.Fatal("want an edit of an agent that is not there refused")
	}
}

// A rename onto a name another file already holds is refused and changes
// nothing, a delete removes the agent's own file and nothing beside it.
func TestARenameOntoAnotherAgentIsRefusedAndADeleteRemovesTheFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.toml"), "name = \"a\"\ndescription = \"A.\"\ndeveloper_instructions = \"x\"\n")
	writeFile(t, filepath.Join(dir, "b.toml"), "name = \"b\"\ndescription = \"B.\"\ndeveloper_instructions = \"x\"\n")
	writeFile(t, filepath.Join(dir, "notes.txt"), "kept")
	r := agentRepository{dir: dir}
	if _, err := r.Save("a", "b", "A.", "x"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want the rename refused, got %v", err)
	}
	if got := agentIDs(r.List()); got != "a,b" {
		t.Fatalf("agents = %s", got)
	}
	deleted, err := r.Delete("a")
	if err != nil || deleted.ID != "a" {
		t.Fatalf("delete = %+v, %v", deleted, err)
	}
	if got := agentIDs(r.List()); got != "b" {
		t.Fatalf("agents = %s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatalf("want the other file kept, got %v", err)
	}
	if _, err := r.Delete("../b"); err == nil {
		t.Fatal("want an unknown agent refused")
	}
}

// A home that keeps its agents elsewhere through a link is read and written
// there, nothing is written outside it.
func TestAnAgentsFolderBehindALinkIsUsed(t *testing.T) {
	real := t.TempDir()
	home := t.TempDir()
	link := filepath.Join(home, "agents")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	r := agentRepository{dir: link}
	if _, err := r.Save("", "linked", "Linked.", "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(real, "linked.toml")); err != nil {
		t.Fatalf("want the agent in the linked folder, got %v", err)
	}
	if got := agentIDs(r.List()); got != "linked" {
		t.Fatalf("agents = %s", got)
	}
}
