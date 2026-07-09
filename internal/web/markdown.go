package web

import (
	"bytes"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// previewMarkdown renders editor buffers (GitHub Flavored Markdown) to HTML.
// Raw HTML rendering stays disabled (the default), so goldmark strips embedded
// HTML and drops dangerous link schemes, which keeps the output safe to inject
// into the page without a separate sanitizer.
var previewMarkdown = goldmark.New(goldmark.WithExtensions(extension.GFM))

func renderMarkdownPreview(src string) (string, error) {
	var buf bytes.Buffer
	if err := previewMarkdown.Convert([]byte(src), &buf); err != nil {
		return "", err
	}
	return buf.String(), nil
}
