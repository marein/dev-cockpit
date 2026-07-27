package web

import "github.com/local/dev-cockpit/internal/markdown"

// renderMarkdownPreview renders editor buffers and chat answers (GitHub
// Flavored Markdown) to HTML. The renderer lives in internal/markdown with raw
// HTML rendering disabled, so the output is safe to inject into the page
// without a separate sanitizer, and the chat stream renders through the same
// one while an answer is still arriving.
func renderMarkdownPreview(src string) (string, error) {
	return markdown.RenderGFM(src)
}
