package distro

import (
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/plugin"
)

type testPlugin struct{}

func (testPlugin) ConfigureServe(s plugin.Serve) error { return nil }

func named(id string) plugin.Named[plugin.ServePlugin] {
	return plugin.Named[plugin.ServePlugin]{ID: id, Plugin: testPlugin{}}
}

func TestValidateWiring(t *testing.T) {
	tests := []struct {
		name    string
		plugins []plugin.Named[plugin.ServePlugin]
		wantErr string
	}{
		{name: "no plugins"},
		{name: "one plugin", plugins: []plugin.Named[plugin.ServePlugin]{named("weather")}},
		{name: "distinct ids", plugins: []plugin.Named[plugin.ServePlugin]{named("weather"), named("github-board2")}},
		{
			name:    "nil plugin",
			plugins: []plugin.Named[plugin.ServePlugin]{named("weather"), {ID: "empty-shell"}},
			wantErr: "plugin 2 is nil",
		},
		{
			name:    "empty id",
			plugins: []plugin.Named[plugin.ServePlugin]{named("")},
			wantErr: "plugin 1 has an empty id",
		},
		{
			name:    "blank id",
			plugins: []plugin.Named[plugin.ServePlugin]{named("  ")},
			wantErr: "not a usable plugin id",
		},
		{
			name:    "uppercase id",
			plugins: []plugin.Named[plugin.ServePlugin]{named("Weather")},
			wantErr: `"Weather" is not a usable plugin id`,
		},
		{
			name:    "id starting with a digit",
			plugins: []plugin.Named[plugin.ServePlugin]{named("2weather")},
			wantErr: `"2weather" is not a usable plugin id`,
		},
		{
			name:    "id with a path step",
			plugins: []plugin.Named[plugin.ServePlugin]{named("weather/eu")},
			wantErr: `"weather/eu" is not a usable plugin id`,
		},
		{
			name:    "duplicate id",
			plugins: []plugin.Named[plugin.ServePlugin]{named("weather"), named("weather")},
			wantErr: `two plugins carry the id "weather"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWiring(tt.plugins)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validateWiring() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateWiring() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateWiring() = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}
