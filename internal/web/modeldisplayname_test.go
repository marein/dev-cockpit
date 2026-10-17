package web

import "testing"

func TestPickDisplayNamesDropThePrefixAndTheCloudSuffixAndNameTheEffort(t *testing.T) {
	cases := []struct{ model, effort, want string }{
		{"ollama/nemotron-3-ultra:cloud", "", "nemotron-3-ultra"},
		{"ollama/gpt-oss:120b-cloud", "", "gpt-oss:120b-cloud"},
		{"opus", "", "opus"},
		{" claude-sonnet-4-5 ", "", "claude-sonnet-4-5"},
		{"ollama/", "", "ollama/"},
		{"", "", ""},
		{"opus", "high", "opus · high effort"},
		{"ollama/nemotron-3-ultra:cloud", "low", "nemotron-3-ultra · low effort"},
		{"", "max", "max effort"},
	}
	for _, c := range cases {
		if got := pickDisplayName(c.model, c.effort); got != c.want {
			t.Errorf("pickDisplayName(%q, %q) = %q, want %q", c.model, c.effort, got, c.want)
		}
	}
}
