package markdown

import (
	"fmt"
	"strings"
	"testing"
)

func headingLines(src string) string {
	var b strings.Builder
	for _, h := range Headings(src) {
		fmt.Fprintf(&b, "%d %q @%q\n", h.Level, h.Text, src[h.Offset:min(h.Offset+4, len(src))])
	}
	return b.String()
}

func TestHeadingsAreTheHashAndUnderlinedOnesInOrder(t *testing.T) {
	src := "Intro.\n\n# One\n## Two ##\n### Three\n#### Four\n##### Five\n###### Six\n####### Seven\n\n" +
		"Equals\n===\n\nDashes\nover two\n---\n\n   ## Indented\n#No space\n"
	want := "1 \"One\" @\"One\\n\"\n2 \"Two\" @\"Two \"\n3 \"Three\" @\"Thre\"\n4 \"Four\" @\"Four\"\n5 \"Five\" @\"Five\"\n6 \"Six\" @\"Six\\n\"\n" +
		"1 \"Equals\" @\"Equa\"\n2 \"Dashes over two\" @\"Dash\"\n2 \"Indented\" @\"Inde\"\n"
	if got := headingLines(src); got != want {
		t.Fatalf("want\n%s\ngot\n%s", want, got)
	}
}

func TestHeadingsLeaveCodeAndHTMLOut(t *testing.T) {
	src := "# Before\n\n```sh\n# a comment\nTitle\n---\n```\n\n~~~~\n## tilde\n```\n## still inside\n~~~~\n\n" +
		"    # indented code\n\n<div>\n# in html\n</div>\n\n# After\n"
	want := "1 \"Before\" @\"Befo\"\n1 \"After\" @\"Afte\"\n"
	if got := headingLines(src); got != want {
		t.Fatalf("want\n%s\ngot\n%s", want, got)
	}
}

func TestHeadingsInsideQuotesAndListsCount(t *testing.T) {
	src := "> ## Quoted\n\n- item\n\n  ### Listed\n"
	want := "2 \"Quoted\" @\"Quot\"\n3 \"Listed\" @\"List\"\n"
	if got := headingLines(src); got != want {
		t.Fatalf("want\n%s\ngot\n%s", want, got)
	}
}

func TestAHeadingSaysItsWordsWithoutTheMarkup(t *testing.T) {
	for src, want := range map[string]string{
		"## The `dc` **command**\n":                       "The dc command",
		"## [1.2.0](https://example.com/compare) (2026)":  "1.2.0 (2026)",
		"## See <https://example.com> and https://x.io\n": "See https://example.com and https://x.io",
		"## Tom &amp; Jerry \\*not em\\* &#35;1\n":        "Tom & Jerry *not em* #1",
		"## Code `a\\*b`\n":                               `Code a\*b`,
		"## A <span>raw</span> tag\n":                     "A raw tag",
		"## ![logo](logo.png) Project\n":                  "logo Project",
		"Two\nlines\n===\n":                               "Two lines",
	} {
		got := Headings(src)
		if len(got) != 1 || got[0].Text != want {
			t.Fatalf("%q: want %q, got %+v", src, want, got)
		}
	}
}

func TestAHeadingWithoutWordsIsLeftOut(t *testing.T) {
	if got := Headings("#\n\n## ##\n\n###   \n\n## `` ``\n"); len(got) != 0 {
		t.Fatalf("want no headings, got %+v", got)
	}
}

func TestAHeadingCarriesThePreviewsID(t *testing.T) {
	src := "# Install\n\n## Install\n\n> ### The `dc` **tool**\n\n#\n\n## 🙂 Notes ##\n\nSetup\n---\n"
	html, err := RenderPreview(src)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, h := range Headings(src) {
		ids = append(ids, h.ID)
		if !strings.Contains(html, `id="user-content-`+h.ID+`"`) {
			t.Fatalf("%q: the preview has no id %q in\n%s", h.Text, h.ID, html)
		}
	}
	if got := strings.Join(ids, " "); got != "install install-1 the-dc-tool -notes setup" {
		t.Fatalf("ids %q", got)
	}
}
