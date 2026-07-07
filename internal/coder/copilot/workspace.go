package copilot

import (
	"errors"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type workspaceMetadata struct {
	ID        string
	Name      string
	CWD       string
	UpdatedAt string
}

func loadWorkspace(path string) (workspaceMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return workspaceMetadata{}, err
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return workspaceMetadata{}, err
	}
	if raw == nil {
		return workspaceMetadata{}, errors.New("empty workspace file")
	}
	return workspaceMetadata{
		ID:        yamlString(raw["id"]),
		Name:      yamlString(raw["name"]),
		CWD:       yamlString(raw["cwd"]),
		UpdatedAt: yamlString(raw["updated_at"]),
	}, nil
}

func yamlString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(t)
	}
	return ""
}
