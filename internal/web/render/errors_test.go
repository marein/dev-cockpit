package render

import (
	"strings"
	"testing"
)

// TestErrorPageRenders executes the real template set with the ErrorPage
// model. ErrorPage deliberately avoids the shared Page model, so every field
// html_head.gohtml evaluates must exist on ErrorPage too. A field added to the
// head without a counterpart on ErrorPage breaks every HTML error response,
// this test catches that at build time.
func TestErrorPageRenders(t *testing.T) {
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test")
	var out strings.Builder
	err := tmpl.ExecuteTemplate(&out, "error.gohtml", ErrorPage{
		Title:   "404 Page not found",
		Status:  404,
		Heading: "Page not found",
		Message: "The page you are looking for does not exist.",
	})
	if err != nil {
		t.Fatalf("render error page: %v", err)
	}
	for _, want := range []string{"404", "Page not found", "</html>"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("error page output misses %q", want)
		}
	}
}
