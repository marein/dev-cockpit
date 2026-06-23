package update

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// notesMarkdown renders GitHub release notes (GitHub Flavored Markdown) to HTML.
// Raw HTML rendering stays disabled (the default), so goldmark strips embedded
// HTML and drops dangerous link schemes, which keeps the output safe to inject
// into the page without a separate sanitizer.
var notesMarkdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

func renderNotes(src string) string {
	if strings.TrimSpace(src) == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := notesMarkdown.Convert([]byte(src), &buf); err != nil {
		return ""
	}
	return buf.String()
}
