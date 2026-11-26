package codex

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/filesystem"
	toml "github.com/pelletier/go-toml/v2"
)

// codex's custom agents are TOML files below agents/ in its home, one agent
// per file: its name, a description of when to use it and its
// developer_instructions, all three required, beside optional settings like
// model (verified on 0.172.0 with `codex debug prompt-input`: a file missing
// one of the three is left out, a file in a folder below agents/ is read too,
// the name and not the file's name is what codex calls it by). codex hands
// work to them as subagents, the roles its spawn_agent tool offers. It starts
// no session as one: neither the interactive CLI nor exec has a flag or a
// setting for it, the documentation names none, and /subagents only switches
// between the threads of agents a session spawned.
type agentRepository struct {
	dir string
}

var errNoAgentStart = errors.New("Codex starts no session as an agent, it runs its agents only as subagents. Pass the role in the prompt instead.")

const agentFileSuffix = ".toml"

// agentFile is the part of an agent file the cockpit edits, written first so
// a person reading the file finds it on top.
type agentFile struct {
	Name                  string `toml:"name"`
	Description           string `toml:"description"`
	DeveloperInstructions string `toml:"developer_instructions,multiline"`
}

var agentKeys = []string{"name", "description", "developer_instructions"}

// root is the agents folder with a link in its own path resolved, so a home
// that keeps its agents elsewhere through a link is read there. Nothing
// below it is followed: a link inside is never read and never written
// through.
func (r agentRepository) root() string {
	if resolved, err := filepath.EvalSymlinks(r.dir); err == nil {
		return resolved
	}
	return filepath.Clean(r.dir)
}

// List answers the agents codex loads, by name. A file codex would leave out
// is left out here too, and of two files naming the same agent the first in
// path order counts.
func (r agentRepository) List() []coder.Agent {
	root := r.root()
	var out []coder.Agent
	seen := map[string]bool{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			return nil
		}
		if !d.Type().IsRegular() || !strings.HasSuffix(d.Name(), agentFileSuffix) {
			return nil
		}
		agent, _, err := readAgent(path)
		if err != nil || seen[agent.ID] {
			return nil
		}
		seen[agent.ID] = true
		out = append(out, agent)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].ID) < strings.ToLower(out[j].ID) })
	return out
}

// Options holds the default alone: codex starts no session with an agent.
func (r agentRepository) Options() []coder.AgentOption {
	return []coder.AgentOption{{Value: "", Label: "Default"}}
}

func (r agentRepository) NoAgentStart() string {
	return "Not available for Codex, it runs its agents only as subagents."
}

func (r agentRepository) ValidateSelected(rawID string) (string, error) {
	if strings.TrimSpace(rawID) != "" {
		return "", errNoAgentStart
	}
	return "", nil
}

func (r agentRepository) Find(rawID string) (coder.Agent, error) {
	id := strings.TrimSpace(rawID)
	if id == "" {
		return coder.Agent{}, errors.New("Agent identifier is required.")
	}
	for _, agent := range r.List() {
		if agent.ID == id {
			return agent, nil
		}
	}
	return coder.Agent{}, fmt.Errorf(`No agent "%s" was found.`, id)
}

// Save writes an agent the way codex reads it. The identifier is checked by
// the same rule as every coder's agents, except that an edit keeps the name
// an agent has when the identifier only spells it the way that rule does:
// codex calls an agent by its name, so `pr_explorer` stays `pr_explorer`.
// An edit keeps the file it found the agent in and every other setting the
// file holds, a rename moves it to <name>.toml in the agents folder.
func (r agentRepository) Save(originalRawID, rawID, rawDesc, rawInstr string) (coder.AgentSaveResult, error) {
	id, err := coder.SanitizeAgentID(rawID)
	if err != nil {
		return coder.AgentSaveResult{}, err
	}
	desc := strings.TrimSpace(rawDesc)
	if desc == "" {
		return coder.AgentSaveResult{}, errors.New("Agent description is required.")
	}
	// A text box posts every line break as CRLF, which a TOML string keeps
	// as a visible \r at the end of each line of the file.
	instructions := strings.TrimSpace(strings.ReplaceAll(rawInstr, "\r\n", "\n"))
	if instructions == "" {
		return coder.AgentSaveResult{}, errors.New("Agent instructions are required.")
	}
	root := r.root()
	var original *coder.Agent
	rest := map[string]any{}
	if originalID := strings.TrimSpace(originalRawID); originalID != "" {
		agent, err := r.Find(originalID)
		if err != nil {
			return coder.AgentSaveResult{}, err
		}
		original = &agent
		if sanitized, err := coder.SanitizeAgentID(agent.ID); err == nil && sanitized == id {
			id = agent.ID
		}
		if _, rest, err = readAgent(agent.Path); err != nil {
			return coder.AgentSaveResult{}, err
		}
	}
	target := filepath.Join(root, id+agentFileSuffix)
	if original != nil && original.ID == id {
		target = original.Path
	}
	if !filesystem.IsUnder(target, root) {
		return coder.AgentSaveResult{}, fmt.Errorf("Refusing to write an agent outside %s.", r.dir)
	}
	for _, agent := range r.List() {
		if agent.ID == id && (original == nil || agent.Path != original.Path) {
			return coder.AgentSaveResult{}, fmt.Errorf(`Agent "%s" already exists.`, id)
		}
	}
	if _, err := os.Lstat(target); err == nil && (original == nil || original.Path != target) {
		return coder.AgentSaveResult{}, fmt.Errorf(`Agent "%s" already exists.`, id)
	}
	content, err := writeAgent(agentFile{Name: id, Description: desc, DeveloperInstructions: instructions}, rest)
	if err != nil {
		return coder.AgentSaveResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return coder.AgentSaveResult{}, err
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return coder.AgentSaveResult{}, err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return coder.AgentSaveResult{}, err
	}
	if original != nil && original.Path != target {
		_ = os.Remove(original.Path)
	}
	saved, err := r.Find(id)
	if err != nil {
		return coder.AgentSaveResult{}, err
	}
	return coder.AgentSaveResult{Saved: saved, Created: original == nil}, nil
}

func (r agentRepository) Delete(rawID string) (coder.Agent, error) {
	agent, err := r.Find(rawID)
	if err != nil {
		return coder.Agent{}, err
	}
	if !filesystem.IsUnder(agent.Path, r.root()) {
		return coder.Agent{}, fmt.Errorf("Refusing to delete an agent outside %s.", r.dir)
	}
	if err := os.Remove(agent.Path); err != nil {
		return coder.Agent{}, err
	}
	return agent, nil
}

// readAgent reads one agent file: the agent as the cockpit shows it and the
// settings beside the three it edits. A file that is no TOML table, or whose
// name, description or developer_instructions is missing, empty or no
// string, is no agent, the way codex leaves it out.
func readAgent(path string) (coder.Agent, map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return coder.Agent{}, nil, err
	}
	var fields map[string]any
	if err := toml.Unmarshal(data, &fields); err != nil {
		return coder.Agent{}, nil, fmt.Errorf("agent file is no TOML table: %s", path)
	}
	values := make([]string, len(agentKeys))
	for i, key := range agentKeys {
		value, _ := fields[key].(string)
		values[i] = strings.TrimSpace(value)
		if values[i] == "" {
			return coder.Agent{}, nil, fmt.Errorf("agent file has no %s: %s", key, path)
		}
		delete(fields, key)
	}
	return coder.Agent{ID: values[0], Description: values[1], Instructions: values[2], Path: path}, fields, nil
}

// writeAgent renders the three edited settings first and the others after
// them as they were read, so a model or a reasoning effort set in the file
// survives an edit. Comments do not, TOML keeps them nowhere a reader sees.
func writeAgent(agent agentFile, rest map[string]any) ([]byte, error) {
	head, err := toml.Marshal(agent)
	if err != nil {
		return nil, err
	}
	if len(rest) == 0 {
		return head, nil
	}
	tail, err := toml.Marshal(rest)
	if err != nil {
		return nil, err
	}
	return bytes.Join([][]byte{head, tail}, []byte("\n")), nil
}
