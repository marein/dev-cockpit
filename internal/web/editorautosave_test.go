package web

import (
	"path/filepath"
	"testing"

	"github.com/marein/dev-cockpit/internal/settings"
)

// Autosave is on where nothing was ever chosen, so only an explicit off
// switches it off.
func TestEditorAutosaveDefault(t *testing.T) {
	cases := []struct {
		stored string
		want   bool
	}{
		{"", true},
		{"on", true},
		{"off", false},
	}
	for _, tc := range cases {
		s := &Server{settings: settings.New(filepath.Join(t.TempDir(), "settings.json"))}
		if tc.stored != "" {
			s.settings.Set(editorAutosaveKey, tc.stored)
		}
		if got := s.editorSettings().Autosave; got != tc.want {
			t.Errorf("stored %q: Autosave = %v, want %v", tc.stored, got, tc.want)
		}
	}
}
