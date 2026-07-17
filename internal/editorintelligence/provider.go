package editorintelligence

import (
	"context"
	"strings"
	"unicode/utf8"
)

// FIM context and output budgets. The prefix and suffix are cut at rune
// boundaries around the cursor; the output caps keep a ghost suggestion a
// short continuation instead of a page of text.
const (
	fimPrefixBytes  = 8 << 10
	fimSuffixBytes  = 2 << 10
	fimMaxChars     = 256
	fimMaxLines     = 12
	fimPredictLimit = 192
)

// FIMRequest is the bounded fill in the middle request handed to an AI
// provider. It deliberately carries no project root, credentials or files
// beyond the active document excerpt.
type FIMRequest struct {
	Language string
	Path     string
	Prefix   string
	Suffix   string
}

// CompletionProvider produces one plain text continuation for a FIM
// request. Available reports usability with a short human readable reason
// when unusable; it must never send source code.
type CompletionProvider interface {
	Available(ctx context.Context) (bool, string)
	Complete(ctx context.Context, req FIMRequest) (string, error)
}

// buildFIMContext cuts the bounded prefix and suffix around the byte offset,
// aligned to rune boundaries.
func buildFIMContext(content string, offset int) (string, string) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(content) {
		offset = len(content)
	}
	prefix := content[:offset]
	suffix := content[offset:]
	if len(prefix) > fimPrefixBytes {
		cut := len(prefix) - fimPrefixBytes
		for cut < len(prefix) && !utf8.RuneStart(prefix[cut]) {
			cut++
		}
		prefix = prefix[cut:]
	}
	if len(suffix) > fimSuffixBytes {
		cut := fimSuffixBytes
		for cut > 0 && !utf8.RuneStart(suffix[cut]) {
			cut--
		}
		suffix = suffix[:cut]
	}
	return prefix, suffix
}

// sanitizeInsertion turns raw model output into a safe plain insertion:
// code fences and leftover control tokens are removed, the output is capped
// by lines and characters, and pure whitespace collapses to nothing.
func sanitizeInsertion(raw string, controlTokens []string) string {
	s := strings.ReplaceAll(raw, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	for _, token := range controlTokens {
		s = strings.ReplaceAll(s, token, "")
	}
	s = stripCodeFences(s)
	lines := strings.Split(s, "\n")
	if len(lines) > fimMaxLines {
		lines = lines[:fimMaxLines]
	}
	s = strings.Join(lines, "\n")
	if utf16Len(s) > fimMaxChars {
		s = cutUTF16(s, fimMaxChars)
		if i := strings.LastIndexByte(s, '\n'); i > 0 {
			s = s[:i]
		}
	}
	s = strings.TrimRight(s, " \t\n")
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return s
}

func cutUTF16(s string, limit int) string {
	units := 0
	for i, r := range s {
		units++
		if r > 0xFFFF {
			units++
		}
		if units > limit {
			return s[:i]
		}
	}
	return s
}

// stripCodeFences drops a wrapping markdown fence a chat tuned model may
// emit despite the FIM prompt.
func stripCodeFences(s string) string {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "```") {
		return s
	}
	lines := strings.Split(trimmed, "\n")
	if len(lines) < 2 {
		return ""
	}
	lines = lines[1:]
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}

// sensitiveNameParts flag a file as withheld when they appear as a whole
// word segment in the file name, so token.json is refused while
// tokenizer.go stays usable.
var sensitiveNameParts = []string{"secret", "secrets", "credential", "credentials", "token", "password", "passwords", "passwd", "apikey"}

// sensitiveExtensions are key and certificate material.
var sensitiveExtensions = map[string]bool{
	".pem": true, ".key": true, ".p12": true, ".pfx": true, ".crt": true,
	".cer": true, ".der": true, ".jks": true, ".keystore": true, ".ppk": true,
	".asc": true, ".gpg": true, ".kdbx": true,
}

// sensitiveDirs are directories whose entire content stays away from AI
// providers.
var sensitiveDirs = map[string]bool{
	".ssh": true, ".gnupg": true, ".aws": true, ".azure": true,
	".kube": true, "secrets": true, "credentials": true,
}

// sensitiveFiles are exact file names that regularly hold credentials.
var sensitiveFiles = map[string]bool{
	".netrc": true, ".npmrc": true, ".pypirc": true, ".htpasswd": true,
	"htpasswd": true, "shadow": true, "id_rsa": true, "id_dsa": true,
	"id_ecdsa": true, "id_ed25519": true,
}

// sensitivePath reports whether AI context for the relative path must be
// withheld. The check is deliberately conservative; the response tells the
// user the completion was withheld instead of silently sending content.
func sensitivePath(rel string) bool {
	rel = strings.ToLower(strings.ReplaceAll(rel, "\\", "/"))
	segments := strings.Split(rel, "/")
	name := segments[len(segments)-1]
	for _, dir := range segments[:len(segments)-1] {
		if sensitiveDirs[dir] {
			return true
		}
	}
	if sensitiveFiles[name] || name == ".env" || strings.HasPrefix(name, ".env.") {
		return true
	}
	if i := strings.LastIndexByte(name, '.'); i >= 0 && sensitiveExtensions[name[i:]] {
		return true
	}
	for _, part := range splitNameParts(name) {
		for _, marker := range sensitiveNameParts {
			if part == marker {
				return true
			}
		}
	}
	return false
}

// splitNameParts splits a file name into alphanumeric runs, so markers only
// match whole segments.
func splitNameParts(name string) []string {
	return strings.FieldsFunc(name, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
}
