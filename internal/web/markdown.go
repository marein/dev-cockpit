package web

import "github.com/marein/dev-cockpit/internal/markdown"

func renderMarkdownPreview(src string) (string, error) {
	return markdown.RenderPreview(src)
}
