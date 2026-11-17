package assistant

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/marein/dev-cockpit/internal/approval"
)

// Each key shows on its own with its value as it will run: a string as its
// text, line breaks and all, anything else as indented JSON, and neither
// escapes what a shell or a page would read as markup.
func TestAToolInputShowsAsItWillRun(t *testing.T) {
	for _, c := range []struct {
		input string
		want  []ToolField
	}{
		{`{"command":"make && ls > out 2>&1 <done>","description":"<@user>","timeout":120000}`, []ToolField{
			{Key: "command", Value: "make && ls > out 2>&1 <done>"},
			{Key: "description", Value: "<@user>"},
			{Key: "timeout", Value: "120000"},
		}},
		{`{"file_path":"/tmp/a.md","content":"# A\n\nline & <b>\n"}`, []ToolField{
			{Key: "file_path", Value: "/tmp/a.md"},
			{Key: "content", Value: "# A\n\nline & <b>\n"},
		}},
		{`{ "edits" : [{"old":"a && b","new":"<c>"}], "all": false, "none": null }`, []ToolField{
			{Key: "edits", Value: "[\n  {\n    \"old\": \"a && b\",\n    \"new\": \"<c>\"\n  }\n]"},
			{Key: "all", Value: "false"},
			{Key: "none", Value: "null"},
		}},
		{`{}`, []ToolField{}},
		{`["a && b"]`, []ToolField{{Value: "[\n  \"a && b\"\n]"}}},
		{`{"a b":"<i>"}`, []ToolField{{Value: "{\n  \"a b\": \"<i>\"\n}"}}},
		{`{"a":1,"a":2}`, []ToolField{{Value: "{\n  \"a\": 1,\n  \"a\": 2\n}"}}},
	} {
		got := ToolFields(json.RawMessage(c.input))
		if !slices.Equal(got, c.want) {
			t.Fatalf("%s shows %+v, want %+v", c.input, got, c.want)
		}
		if err := CheckToolFields(got); err != nil {
			t.Fatalf("%s shows fields the server refuses: %v", c.input, err)
		}
	}
}

// A value beyond the dialog's bound shows its first 64 KiB, cut on a
// character, and the rest of the input whole. An input that does not split
// into a few keys within the bound shows whole as JSON in one field, cut the
// same way.
func TestALargeToolInputShowsCutWhereTheDialogCuts(t *testing.T) {
	content := strings.Repeat("ü", 1<<19)
	got := ToolFields(asCoderWrites(t, map[string]string{"file_path": "/tmp/big.txt", "content": content}))
	if len(got) != 2 || got[0] != (ToolField{Key: "content", Value: got[0].Value, Cut: true}) || got[1] != (ToolField{Key: "file_path", Value: "/tmp/big.txt"}) ||
		len(got[0].Value) > approval.MaxDetailBytes || len(got[0].Value) < approval.MaxDetailBytes-utf8.UTFMax || !utf8.ValidString(got[0].Value) || !strings.HasPrefix(content, got[0].Value) {
		t.Fatalf("a long value shows %d bytes of %d fields, cut %v", len(got[0].Value), len(got), got[0].Cut)
	}
	many := map[string]string{}
	for i := range maxToolFields + 1 {
		many[fmt.Sprintf("k%02d", i)] = "<v>"
	}
	wide := map[string]string{}
	for i := range 5 {
		wide[fmt.Sprintf("k%d", i)] = strings.Repeat("&", approval.MaxDetailBytes)
	}
	for name, input := range map[string]any{"many keys": many, "long values": wide, "a long key": map[string]int{strings.Repeat("k", maxToolKey+1): 1}} {
		got := ToolFields(asCoderWrites(t, input))
		if len(got) != 1 || got[0].Key != "" || strings.Contains(got[0].Value, `\u00`) || !strings.HasPrefix(got[0].Value, "{\n  \"") ||
			got[0].Cut != (len(got[0].Value) == approval.MaxDetailBytes) || CheckToolFields(got) != nil {
			t.Fatalf("%s shows %d fields, the first %.80q cut %v", name, len(got), got[0].Value, got[0].Cut)
		}
	}
}

// asCoderWrites is an input in the JSON a coder sends, which escapes no markup.
func asCoderWrites(t *testing.T, input any) json.RawMessage {
	t.Helper()
	var out strings.Builder
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(input); err != nil {
		t.Fatal(err)
	}
	return json.RawMessage(out.String())
}

// The server takes what ToolFields makes and nothing past it, and the request
// carrying the most it makes, in the letters JSON writes longest, fits the
// body the server reads.
func TestTheServerTakesWhatAToolInputShows(t *testing.T) {
	worst := make([]ToolField, maxToolFields)
	for i := range worst {
		worst[i] = ToolField{Key: fmt.Sprintf("%0*d", maxToolKey, i)}
	}
	for i := range ToolInputShown / approval.MaxDetailBytes {
		worst[i].Value, worst[i].Cut = strings.Repeat("\x01", approval.MaxDetailBytes), true
	}
	if err := CheckToolFields(worst); err != nil {
		t.Fatalf("the largest input is refused: %v", err)
	}
	body, _ := json.Marshal(map[string]any{"tool": strings.Repeat("T", 200), "call": strings.Repeat("c", 128), "input": worst})
	if len(body) > ToolApprovalBody {
		t.Fatalf("the largest request takes %d bytes, the server reads %d", len(body), ToolApprovalBody)
	}
	for name, fields := range map[string][]ToolField{
		"too many keys":                append(slices.Clone(worst), ToolField{Key: "more"}),
		"a key with a space":           {{Key: "a b"}},
		"a key too long":               {{Key: strings.Repeat("k", maxToolKey+1)}},
		"a keyless field among others": {{Key: "a"}, {Value: "{}"}},
		"a value too long":             {{Key: "a", Value: strings.Repeat("x", approval.MaxDetailBytes+1)}},
		"too much in all": {{Key: "a", Value: strings.Repeat("x", approval.MaxDetailBytes)}, {Key: "b", Value: strings.Repeat("x", approval.MaxDetailBytes)},
			{Key: "c", Value: strings.Repeat("x", approval.MaxDetailBytes)}, {Key: "d", Value: strings.Repeat("x", approval.MaxDetailBytes)}, {Key: "e", Value: "x"}},
	} {
		if CheckToolFields(fields) == nil {
			t.Fatalf("%s was taken", name)
		}
	}
}
