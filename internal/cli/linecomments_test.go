package cli

import (
	"strings"
	"testing"
)

func lineCommentsAnswer(entries ...map[string]any) map[string]any {
	raw := make([]any, 0, len(entries))
	for _, entry := range entries {
		raw = append(raw, any(entry))
	}
	return map[string]any{"comments": raw}
}

func TestFormatLineCommentsListsFileLineQuoteAndId(t *testing.T) {
	out := formatLineComments("proj", nil, "", false, lineCommentsAnswer(
		map[string]any{"id": "aa11", "path": "notes.txt", "line": float64(3), "lineText": "two", "text": "needs a guard"},
		map[string]any{"id": "bb22", "path": "sub/x.go", "line": float64(7), "text": "first\nsecond"},
	))
	for _, want := range []string{
		"Line comments in \"proj\" (2)",
		"notes.txt:3  id aa11",
		"> two",
		"sub/x.go:7  id bb22",
		"  first\n",
		"  second\n",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output misses %q:\n%s", want, out)
		}
	}
}

func TestFormatLineCommentsCapsAndFiltersBeforeIt(t *testing.T) {
	entries := make([]map[string]any, 0, maxLineCommentsShown+3)
	for i := 0; i < maxLineCommentsShown+2; i++ {
		entries = append(entries, map[string]any{"id": "x", "path": "a.txt", "line": float64(i + 1), "text": "filler"})
	}
	entries = append(entries, map[string]any{"id": "deep", "path": "internal/deep/b.go", "line": float64(9), "text": "the one"})
	plain := formatLineComments("proj", nil, "", false, lineCommentsAnswer(entries...))
	if !strings.Contains(plain, "and 3 more, narrow the list with --path, --contains or --outdated") {
		t.Fatalf("the dropped tail is not counted:\n%s", plain)
	}
	if strings.Contains(plain, "id deep") {
		t.Fatalf("the capped list shows the tail:\n%s", plain)
	}
	byWord := formatLineComments("proj", nil, "THE ONE", false, lineCommentsAnswer(entries...))
	if !strings.Contains(byWord, "id deep") || !strings.Contains(byWord, "(1)") {
		t.Fatalf("--contains does not filter before the cap or compare case insensitively:\n%s", byWord)
	}
}

func TestKeepsLineCommentSearchesNoteAndQuote(t *testing.T) {
	entry := lineCommentEntry{Path: "internal/a.go", Text: "note", LineText: "code"}
	if !keepsLineComment(entry, "CODE") {
		t.Fatal("the quoted line is not searched")
	}
	if !keepsLineComment(entry, "note") {
		t.Fatal("the note is not searched")
	}
	if keepsLineComment(entry, "absent") {
		t.Fatal("an absent word matches")
	}
}

func TestFormatLineCommentsMarksAndFiltersOutdated(t *testing.T) {
	answer := lineCommentsAnswer(
		map[string]any{"id": "fresh", "path": "a.txt", "line": float64(1), "lineText": "one", "text": "fine"},
		map[string]any{"id": "stale", "path": "a.txt", "line": float64(4), "lineText": "gone line", "text": "orphan", "outdated": true},
	)
	all := formatLineComments("proj", nil, "", false, answer)
	if !strings.Contains(all, "a.txt:4  id stale  outdated") {
		t.Fatalf("the outdated entry is not marked:\n%s", all)
	}
	if strings.Contains(all, "id fresh  outdated") {
		t.Fatalf("a fresh entry reads as outdated:\n%s", all)
	}
	only := formatLineComments("proj", nil, "", true, answer)
	if !strings.Contains(only, "outdated only (1)") || strings.Contains(only, "id fresh") {
		t.Fatalf("--outdated does not narrow the list:\n%s", only)
	}
}

func TestFormatLineCommentsEmptyNamesTheFilters(t *testing.T) {
	out := formatLineComments("proj", []string{"sub", "**/x.go"}, "word", false, lineCommentsAnswer())
	if !strings.Contains(out, `under "sub, **/x.go"`) || !strings.Contains(out, `containing "word"`) || !strings.Contains(out, "none") {
		t.Fatalf("the empty filtered list does not say what was asked:\n%s", out)
	}
}
