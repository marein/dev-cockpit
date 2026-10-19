package markdown

import (
	"strings"
	"testing"
)

func TestAQuoteWithAMarkerBecomesAnAlert(t *testing.T) {
	out := render(t, "> [!tip]\n>\n> One.\n>\n> **Two**.\n")
	for _, want := range []string{
		`<blockquote class="border-green">`,
		`<p class="text-green fw-medium mb-2"><i aria-hidden="true" class="ti ti-bulb"></i> Tip</p>`,
		"<p>One.</p>", "<p><strong>Two</strong>.</p>",
	} {
		if !strings.Contains(out, want) || strings.Contains(out, "[!") {
			t.Fatalf("want %s and the marker gone in %q", want, out)
		}
	}
}

func TestOtherQuotesStayQuotes(t *testing.T) {
	for _, src := range []string{
		"> [!UNKNOWN]\n> x\n",
		"> [!TIP] same line\n> body\n",
		"> [!WARNING]\n",
		"- item\n\n  > [!NOTE]\n  > in a list\n",
	} {
		if out := render(t, src); !strings.Contains(out, "<blockquote>") || !strings.Contains(out, "[!") {
			t.Fatalf("want a plain quote for %q, got %q", src, out)
		}
	}
}
