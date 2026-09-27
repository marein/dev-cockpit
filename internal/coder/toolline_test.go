package coder

import (
	"strings"
	"testing"
)

// One table serves every coder's spelling of a tool and its arguments.
func TestAToolLineReadsEveryCodersSpelling(t *testing.T) {
	cases := []struct {
		name, input, want string
	}{
		{"Bash", `{"command":"ls /tmp\nmore"}`, "ls /tmp"},
		{"bash", `{"command":"ls","description":"List"}`, "ls"},
		{"Read", `{"file_path":"/a.go"}`, "/a.go"},
		{"read", `{"filePath":"/b.go"}`, "/b.go"},
		{"view", `{"path":"/c.go","view_range":[1,2]}`, "/c.go"},
		{"Grep", `{"pattern":"TODO"}`, "TODO"},
		{"rg", `{"pattern":"x","paths":["."]}`, "x"},
		{"web_fetch", `{"url":"https://example.org"}`, "https://example.org"},
		{"apply_patch", `"*** Begin Patch\n*** Update File: /d.py\n@@"`, "/d.py"},
		{"apply_patch", `{"patchText":"*** Begin Patch\n*** Add File: /e.txt\n+x"}`, "/e.txt"},
		{"TodoWrite", `{"todos":[{"content":"x"},{"content":"y"}]}`, "2 items"},
		{"todowrite", `{"todos":3,"command":null}`, "3 items"},
		{"todowrite", `{"todos":1}`, "1 item"},
		{"todowrite", `{"todos":null}`, ""},
		{"mystery", `{"anything":"at all"}`, ""},
		{"Bash", `not json`, ""},
	}
	for _, c := range cases {
		if got := ToolLine(c.name, []byte(c.input)); got != c.want {
			t.Errorf("%s %s: got %q, want %q", c.name, c.input, got, c.want)
		}
	}
	long := strings.Repeat("x", 300)
	if got := ToolLine("bash", []byte(`{"command":"`+long+`"}`)); len([]rune(got)) != ToolLineRunes+1 {
		t.Fatalf("a long line is cut: %d runes", len([]rune(got)))
	}
}
