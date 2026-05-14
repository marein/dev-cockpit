package provider

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/local/dev-cockpit/internal/filesystem"
	"github.com/local/dev-cockpit/internal/markdown"
	"gopkg.in/yaml.v3"
)

// AgentRepository manages provider-specific stored agents.
type AgentRepository interface {
	List() []Agent
	Options() []AgentOption
	Find(rawID string) (Agent, error)
	ValidateSelected(rawID string) (string, error)
	Save(originalRawID, rawID, rawDesc, rawInstr string) (AgentSaveResult, error)
	Delete(rawID string) (Agent, error)
}

// Agent is a stored provider-specific agent definition.
type Agent struct {
	ID           string
	Description  string
	Instructions string
	Path         string
}

// AgentOption is a UI-friendly choice for the agent dropdown.
type AgentOption struct {
	Value string
	Label string
}

// AgentSaveResult conveys what happened when saving an agent.
type AgentSaveResult struct {
	Saved   Agent
	Created bool
}

type standardAgentFrontMatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

type standardAgentRepository struct {
	dir        string
	fileSuffix string
}

func NewStandardAgentRepository(dir, fileSuffix string) AgentRepository {
	return standardAgentRepository{dir: dir, fileSuffix: fileSuffix}
}

func SanitizeAgentID(raw string) (string, error) {
	out := filesystem.ToDirectoryName(raw)
	if out == "" {
		return "", errors.New("ID must include at least one letter or number.")
	}
	return out, nil
}

func (r standardAgentRepository) List() []Agent {
	info, err := os.Stat(r.dir)
	if err != nil || !info.IsDir() {
		return nil
	}
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return nil
	}
	out := make([]Agent, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), strings.ToLower(r.fileSuffix)) {
			continue
		}
		agent, err := r.parseFile(filepath.Join(r.dir, entry.Name()))
		if err != nil || seen[agent.ID] {
			continue
		}
		seen[agent.ID] = true
		out = append(out, agent)
	}
	sort.Slice(out, func(i, j int) bool {
		left := strings.ToLower(out[i].ID)
		right := strings.ToLower(out[j].ID)
		if left == right {
			return out[i].Path < out[j].Path
		}
		return left < right
	})
	return out
}

func (r standardAgentRepository) Options() []AgentOption {
	opts := []AgentOption{{Value: "", Label: "Default"}}
	for _, agent := range r.List() {
		opts = append(opts, AgentOption{Value: agent.ID, Label: agent.ID})
	}
	return opts
}

func (r standardAgentRepository) Find(rawID string) (Agent, error) {
	id := strings.TrimSpace(rawID)
	if id == "" {
		return Agent{}, errors.New("Agent identifier is required.")
	}
	for _, agent := range r.List() {
		if agent.ID == id {
			return agent, nil
		}
	}
	return Agent{}, fmt.Errorf(`No agent "%s" was found.`, id)
}

func (r standardAgentRepository) ValidateSelected(rawID string) (string, error) {
	id := strings.TrimSpace(rawID)
	for _, opt := range r.Options() {
		if opt.Value == id {
			return id, nil
		}
	}
	return "", fmt.Errorf(`Selected agent is not available: "%s".`, id)
}

func (r standardAgentRepository) Save(originalRawID, rawID, rawDesc, rawInstr string) (AgentSaveResult, error) {
	originalID := strings.TrimSpace(originalRawID)
	id, err := SanitizeAgentID(rawID)
	if err != nil {
		return AgentSaveResult{}, err
	}
	desc := strings.TrimSpace(rawDesc)
	if desc == "" {
		return AgentSaveResult{}, errors.New("Agent description is required.")
	}
	instructions := strings.TrimSpace(rawInstr)
	if instructions == "" {
		return AgentSaveResult{}, errors.New("Agent instructions are required.")
	}
	var originalAgent *Agent
	if originalID != "" {
		agent, err := r.Find(originalID)
		if err != nil {
			return AgentSaveResult{}, err
		}
		originalAgent = &agent
	}
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		return AgentSaveResult{}, err
	}
	target := filepath.Join(r.dir, id+r.fileSuffix)
	absDir, _ := filepath.Abs(r.dir)
	absTarget, _ := filepath.Abs(target)
	if !filesystem.IsUnder(absTarget, absDir) {
		return AgentSaveResult{}, fmt.Errorf("Refusing to write an agent outside %s.", r.dir)
	}
	for _, agent := range r.List() {
		if agent.ID == id && (originalAgent == nil || agent.Path != originalAgent.Path) {
			return AgentSaveResult{}, fmt.Errorf(`Agent "%s" already exists.`, id)
		}
	}
	if _, err := os.Stat(absTarget); err == nil {
		if originalAgent == nil || originalAgent.Path != absTarget {
			return AgentSaveResult{}, fmt.Errorf(`Agent "%s" already exists.`, id)
		}
	}
	content, err := markdown.WriteFrontMatter(standardAgentFrontMatter{Name: id, Description: desc}, instructions)
	if err != nil {
		return AgentSaveResult{}, err
	}
	tmp := absTarget + ".tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return AgentSaveResult{}, err
	}
	if err := os.Rename(tmp, absTarget); err != nil {
		return AgentSaveResult{}, err
	}
	if originalAgent != nil && originalAgent.Path != absTarget {
		_ = os.Remove(originalAgent.Path)
	}
	saved, err := r.Find(id)
	if err != nil {
		return AgentSaveResult{}, err
	}
	return AgentSaveResult{Saved: saved, Created: originalAgent == nil}, nil
}

func (r standardAgentRepository) Delete(rawID string) (Agent, error) {
	agent, err := r.Find(rawID)
	if err != nil {
		return Agent{}, err
	}
	absDir, _ := filepath.Abs(r.dir)
	if !filesystem.IsUnder(agent.Path, absDir) {
		return Agent{}, fmt.Errorf("Refusing to delete an agent outside %s.", r.dir)
	}
	if err := os.Remove(agent.Path); err != nil {
		return Agent{}, err
	}
	return agent, nil
}

func (r standardAgentRepository) parseFile(path string) (Agent, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Agent{}, err
	}
	meta, body := markdown.SplitFrontMatter(data)
	var fm standardAgentFrontMatter
	if len(meta) > 0 {
		_ = yaml.Unmarshal(meta, &fm)
	}
	id := strings.TrimSpace(fm.Name)
	if id == "" {
		id = filepath.Base(path)
		if strings.HasSuffix(strings.ToLower(id), strings.ToLower(r.fileSuffix)) {
			id = id[:len(id)-len(r.fileSuffix)]
		}
	}
	id, err = SanitizeAgentID(id)
	if err != nil {
		return Agent{}, fmt.Errorf("agent file has no valid identifier: %s", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return Agent{}, err
	}
	return Agent{
		ID:           id,
		Description:  strings.TrimSpace(fm.Description),
		Instructions: strings.TrimSpace(string(body)),
		Path:         abs,
	}, nil
}
