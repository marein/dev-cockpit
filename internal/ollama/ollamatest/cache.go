package ollamatest

import (
	"path/filepath"
	"time"

	"github.com/marein/dev-cockpit/internal/ollama"
	"github.com/marein/dev-cockpit/internal/statefile"
)

func Cache(stateDir string, server, catalog []string, windows map[string]int, reachable bool) {
	statefile.Save(filepath.Join(stateDir, ollama.CacheFile), 0o644, map[string]any{
		"names":     server,
		"windows":   windows,
		"catalog":   catalog,
		"catalogAt": time.Now(),
		"reachable": reachable,
		"checkedAt": time.Now(),
	})
}
