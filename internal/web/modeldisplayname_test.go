package web

import "testing"

func TestModelDisplayNamesDropThePrefixAndTheCloudSuffix(t *testing.T) {
	cases := map[string]string{
		"ollama/nemotron-3-ultra:cloud": "nemotron-3-ultra",
		"ollama/gpt-oss:120b-cloud":     "gpt-oss:120b-cloud",
		"opus":                          "opus",
		" claude-sonnet-4-5 ":           "claude-sonnet-4-5",
		"ollama/":                       "ollama/",
		"":                              "",
	}
	for raw, want := range cases {
		if got := modelDisplayName(raw); got != want {
			t.Errorf("modelDisplayName(%q) = %q, want %q", raw, got, want)
		}
	}
}
