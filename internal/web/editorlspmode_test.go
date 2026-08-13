package web

import "testing"

// The automatic default and the explicit picks: the two stored values mean
// what they always meant regardless of what can run, everything else is
// Docker while it can run, else off, quietly.
func TestResolveLSPMode(t *testing.T) {
	cases := []struct {
		name     string
		stored   string
		dockerOK bool
		wantOff  bool
	}{
		{"explicit off stays off", "off", true, true},
		{"explicit docker stays docker even unreachable", "gopls-docker", false, false},
		{"absent runs docker while it can", "", true, false},
		{"absent is off without docker", "", false, true},
		{"stored auto is the same default", "auto", true, false},
		{"unknown value is the same default", "someday-runtime", false, true},
	}
	for _, tc := range cases {
		if got := resolveLSPMode(tc.stored, "gopls", tc.dockerOK); got != tc.wantOff {
			t.Errorf("%s: resolveLSPMode(%q) = %v, want %v", tc.name, tc.stored, got, tc.wantOff)
		}
	}
}
