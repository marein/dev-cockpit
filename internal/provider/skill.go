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

// SkillRepository manages provider-specific global skills.
type SkillRepository interface {
	List() []Skill
	Find(rawID string) (Skill, error)
	Save(originalRawID, rawID, rawDesc, rawInstr string) (SkillSaveResult, error)
	Delete(rawID string) (Skill, error)
}

// Skill is a stored provider-specific global skill definition.
type Skill struct {
	ID           string
	Description  string
	Instructions string
	Path         string
}

// SkillSaveResult conveys what happened when saving a skill.
type SkillSaveResult struct {
	Saved   Skill
	Created bool
}

const standardSkillFileName = "SKILL.md"

type standardSkillFrontMatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

type standardSkillRepository struct {
	dir string
}

func NewStandardSkillRepository(dir string) SkillRepository {
	return standardSkillRepository{dir: dir}
}

func SanitizeSkillID(raw string) (string, error) {
	out := filesystem.ToDirectoryName(raw)
	if out == "" {
		return "", errors.New("ID must include at least one letter or number.")
	}
	return out, nil
}

func (r standardSkillRepository) List() []Skill {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return nil
	}
	out := make([]Skill, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skill, err := r.parseFile(filepath.Join(r.dir, entry.Name(), standardSkillFileName))
		if err != nil {
			continue
		}
		out = append(out, skill)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].ID) < strings.ToLower(out[j].ID) })
	return out
}

func (r standardSkillRepository) Find(rawID string) (Skill, error) {
	id := strings.TrimSpace(rawID)
	if id == "" {
		return Skill{}, errors.New("Skill identifier is required.")
	}
	for _, skill := range r.List() {
		if skill.ID == id {
			return skill, nil
		}
	}
	return Skill{}, fmt.Errorf(`No skill "%s" was found.`, id)
}

func (r standardSkillRepository) Save(originalRawID, rawID, rawDesc, rawInstr string) (SkillSaveResult, error) {
	originalID := strings.TrimSpace(originalRawID)
	id, err := SanitizeSkillID(rawID)
	if err != nil {
		return SkillSaveResult{}, err
	}
	desc := strings.TrimSpace(rawDesc)
	if desc == "" {
		return SkillSaveResult{}, errors.New("Skill description is required.")
	}
	instructions := strings.TrimSpace(rawInstr)
	if instructions == "" {
		return SkillSaveResult{}, errors.New("Skill instructions are required.")
	}
	var originalSkill *Skill
	if originalID != "" {
		skill, err := r.Find(originalID)
		if err != nil {
			return SkillSaveResult{}, err
		}
		originalSkill = &skill
	}
	target := filepath.Join(r.dir, id, standardSkillFileName)
	absDir, _ := filepath.Abs(r.dir)
	absTarget, _ := filepath.Abs(target)
	if !filesystem.IsUnder(absTarget, absDir) {
		return SkillSaveResult{}, fmt.Errorf("Refusing to write a skill outside %s.", r.dir)
	}
	if originalSkill != nil && originalSkill.Path != absTarget {
		oldDir := filepath.Dir(originalSkill.Path)
		newDir := filepath.Dir(absTarget)
		if oldDir != newDir {
			if _, err := os.Stat(newDir); err == nil {
				return SkillSaveResult{}, fmt.Errorf(`Skill "%s" already exists.`, id)
			}
			if err := os.MkdirAll(filepath.Dir(newDir), 0o755); err != nil {
				return SkillSaveResult{}, err
			}
			if err := os.Rename(oldDir, newDir); err != nil {
				return SkillSaveResult{}, err
			}
			originalSkill.Path = absTarget
		}
	}
	if err := os.MkdirAll(filepath.Dir(absTarget), 0o755); err != nil {
		return SkillSaveResult{}, err
	}
	if _, err := os.Stat(absTarget); err == nil {
		if originalSkill == nil || originalSkill.Path != absTarget {
			return SkillSaveResult{}, fmt.Errorf(`Skill "%s" already exists.`, id)
		}
	}
	content, err := markdown.WriteFrontMatter(standardSkillFrontMatter{Name: id, Description: desc}, instructions)
	if err != nil {
		return SkillSaveResult{}, err
	}
	tmp := absTarget + ".tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return SkillSaveResult{}, err
	}
	if err := os.Rename(tmp, absTarget); err != nil {
		return SkillSaveResult{}, err
	}
	saved, err := r.Find(id)
	if err != nil {
		return SkillSaveResult{}, err
	}
	return SkillSaveResult{Saved: saved, Created: originalSkill == nil}, nil
}

func (r standardSkillRepository) Delete(rawID string) (Skill, error) {
	skill, err := r.Find(rawID)
	if err != nil {
		return Skill{}, err
	}
	absDir, _ := filepath.Abs(r.dir)
	skillDir := filepath.Dir(skill.Path)
	if skillDir == absDir || !filesystem.IsUnder(skillDir, absDir) {
		return Skill{}, fmt.Errorf("Refusing to delete a skill outside %s.", r.dir)
	}
	if err := os.RemoveAll(skillDir); err != nil {
		return Skill{}, err
	}
	return skill, nil
}

func (r standardSkillRepository) parseFile(path string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	meta, body := markdown.SplitFrontMatter(data)
	var fm standardSkillFrontMatter
	if len(meta) > 0 {
		_ = yaml.Unmarshal(meta, &fm)
	}
	id := strings.TrimSpace(fm.Name)
	if id == "" {
		id = filepath.Base(filepath.Dir(path))
	}
	id, err = SanitizeSkillID(id)
	if err != nil {
		return Skill{}, fmt.Errorf("skill file has no valid identifier: %s", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return Skill{}, err
	}
	return Skill{
		ID:           id,
		Description:  strings.TrimSpace(fm.Description),
		Instructions: strings.TrimSpace(string(body)),
		Path:         abs,
	}, nil
}
