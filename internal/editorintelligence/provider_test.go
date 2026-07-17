package editorintelligence

import (
	"strings"
	"testing"
)

func TestBuildFIMContextBounds(t *testing.T) {
	content := strings.Repeat("ä", 10000)
	offset := len(content) - 100
	prefix, suffix := buildFIMContext(content, offset)
	if len(prefix) > fimPrefixBytes || len(suffix) > fimSuffixBytes {
		t.Fatalf("bounds: prefix %d suffix %d", len(prefix), len(suffix))
	}
	for _, s := range []string{prefix, suffix} {
		if !strings.HasPrefix(s, "ä") || !strings.HasSuffix(s, "ä") {
			t.Fatal("cut split a rune")
		}
	}
	prefix, suffix = buildFIMContext("short", 2)
	if prefix != "sh" || suffix != "ort" {
		t.Fatalf("short doc: %q %q", prefix, suffix)
	}
	prefix, suffix = buildFIMContext("x", 99)
	if prefix != "x" || suffix != "" {
		t.Fatalf("clamped offset: %q %q", prefix, suffix)
	}
}

func TestSanitizeInsertion(t *testing.T) {
	if got := sanitizeInsertion("  \n\t\n", nil); got != "" {
		t.Fatalf("whitespace must collapse, got %q", got)
	}
	if got := sanitizeInsertion("value<|endoftext|>", []string{"<|endoftext|>"}); got != "value" {
		t.Fatalf("control token: %q", got)
	}
	if got := sanitizeInsertion("```go\ncode()\n```", nil); got != "code()" {
		t.Fatalf("fences: %q", got)
	}
	if got := sanitizeInsertion("a\r\nb\rc", nil); got != "a\nb\nc" {
		t.Fatalf("line endings: %q", got)
	}
	long := strings.Repeat("line\n", fimMaxLines+10)
	if got := sanitizeInsertion(long, nil); strings.Count(got, "\n") > fimMaxLines-1 {
		t.Fatalf("line cap: %d lines", strings.Count(got, "\n")+1)
	}
	wide := strings.Repeat("x", fimMaxChars+50)
	if got := sanitizeInsertion(wide, nil); len(got) > fimMaxChars {
		t.Fatalf("char cap: %d", len(got))
	}
	if got := sanitizeInsertion("keep\ntrail  \t", nil); got != "keep\ntrail" {
		t.Fatalf("trailing whitespace: %q", got)
	}
}

func TestSensitivePath(t *testing.T) {
	sensitive := []string{
		".env",
		".env.local",
		"config/.env.production",
		"certs/server.key",
		"certs/server.pem",
		"deploy/id_rsa",
		".ssh/known_hosts",
		"secrets/anything.txt",
		"app/credentials/db.yaml",
		"api_token.txt",
		"token.json",
		"passwords.txt",
		"my-secret-config.yaml",
		".npmrc",
	}
	for _, p := range sensitive {
		if !sensitivePath(p) {
			t.Fatalf("%q must be sensitive", p)
		}
	}
	harmless := []string{
		"main.go",
		"tokenizer.go",
		"internal/web/handlers_editor.go",
		"environment.md",
		"docs/tokens-explained.md",
		"src/secretary.php",
		"styles/main.scss",
	}
	for _, p := range harmless {
		if sensitivePath(p) {
			t.Fatalf("%q must not be sensitive", p)
		}
	}
}
