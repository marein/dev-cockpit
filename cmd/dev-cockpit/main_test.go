package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/local/dev-cockpit/internal/update"
)

// The compiled default of the updateFeedFormat build var must resolve, because
// main refuses every invocation of a binary whose value does not: a default
// that stopped parsing would brick plain builds.
func TestBuildDefaultUpdateFeedFormatResolves(t *testing.T) {
	if _, err := update.ParseFeedFormat(updateFeedFormat); err != nil {
		t.Fatalf("default updateFeedFormat does not resolve: %v", err)
	}
}

// --version must print the injected values and exit clean whatever they
// carry: the updater's smoke test before a swap reads exactly that exit code,
// and a value with template syntax used to break the parse of the version
// template. The values travel as quoted template string literals now, so they
// print literally. All three build vars are mutated: a format with template
// syntax never passes the check in main, but the template must not lean on
// that order.
func TestVersionFlagSurvivesInjectedTemplateSyntax(t *testing.T) {
	restoreRepo, restoreFormat, restoreFeed := repoURL, updateFeedFormat, updateFeedURL
	defer func() { repoURL, updateFeedFormat, updateFeedURL = restoreRepo, restoreFormat, restoreFeed }()
	for name, injected := range map[string]struct{ repo, format, feed string }{
		"default values": {restoreRepo, restoreFormat, restoreFeed},
		"template syntax": {
			repo:   `https://example.test/{{define "x"}}?q="a"&r=1`,
			format: `{{if}}`,
			feed:   `https://feed.test/{{end}}?page={{ .broken`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			repoURL, updateFeedFormat, updateFeedURL = injected.repo, injected.format, injected.feed
			cmd := newRootCommand()
			cmd.SetArgs([]string{"--version"})
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("--version failed: %v", err)
			}
			for _, want := range []string{"dev-cockpit version ", "repoURL: " + injected.repo, "updateFeedFormat: " + injected.format, "updateFeedURL: " + injected.feed} {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("output %q does not carry %q", out.String(), want)
				}
			}
		})
	}
}
