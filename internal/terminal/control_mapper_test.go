package terminal

import "testing"

func TestDefaultMapperMapsNamedAndPrefixedKeys(t *testing.T) {
	cases := map[string]string{
		"enter":     "Enter",
		"shift-tab": "BTab",
		"ctrl-c":    "C-c",
		"alt-enter": "M-Enter",
	}
	m := DefaultControlMapper()
	for raw, want := range cases {
		mapped, ok := m.Map(raw)
		if !ok || mapped != want {
			t.Fatalf("Map(%q) = %q, %v, want %q", raw, mapped, ok, want)
		}
	}
}
