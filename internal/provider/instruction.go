package provider

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// GlobalInstructions reads and writes provider-wide instructions.
type GlobalInstructions interface {
	Read() (string, error)
	Save(raw string) error
}

type fileGlobalInstructions struct {
	filePath string
}

func NewFileGlobalInstructions(filePath string) GlobalInstructions {
	return fileGlobalInstructions{filePath: filePath}
}

func (g fileGlobalInstructions) Read() (string, error) {
	data, err := os.ReadFile(g.filePath)
	if err == nil {
		return string(data), nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return "", err
}

func (g fileGlobalInstructions) Save(raw string) error {
	if strings.TrimSpace(g.filePath) == "" {
		return errors.New("Instruction file path is not configured.")
	}
	if err := os.MkdirAll(filepath.Dir(g.filePath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(g.filePath, []byte(raw), 0o644)
}
