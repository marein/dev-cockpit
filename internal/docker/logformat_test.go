package docker

import (
	"io"
	"regexp"
	"strings"
	"testing"
)

// formatLines runs the formatter over the given lines and answers what came
// out, one entry per output line.
func formatLines(t *testing.T, input []string, pattern string, context int) []string {
	t.Helper()
	var out strings.Builder
	if err := FormatLogs(strings.NewReader(strings.Join(input, "\n")+"\n"), &out, pattern, context); err != nil {
		t.Fatalf("FormatLogs answered %v", err)
	}
	text := strings.TrimSuffix(out.String(), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

var sgrCode = regexp.MustCompile("\x1b\\[[0-9;]*m")

// plainLine strips every escape sequence, what remains is the text a person
// reads.
func plainLine(line string) string {
	return sgrCode.ReplaceAllString(line, "")
}

// serviceTint answers the 256 color code a line is tinted with, empty when
// the line carries none.
func serviceTint(line string) string {
	m := regexp.MustCompile(`38;5;(\d+)m`).FindStringSubmatch(line)
	if m == nil {
		return ""
	}
	return m[1]
}

func TestLogFormatterServiceTintStability(t *testing.T) {
	lines := formatLines(t, []string{
		"web-1  | starting",
		"db-1   | ready",
		"web-1  | listening",
	}, "", 2)
	if len(lines) != 3 {
		t.Fatalf("formatted %d lines: %q", len(lines), lines)
	}
	if serviceTint(lines[0]) == "" {
		t.Fatalf("the prefixed line carries no tint: %q", lines[0])
	}
	if serviceTint(lines[0]) != serviceTint(lines[2]) {
		t.Fatalf("one service got two tints: %q vs %q", lines[0], lines[2])
	}
	if serviceTint(lines[0]) == serviceTint(lines[1]) {
		t.Fatalf("web-1 and db-1 share a tint: %q vs %q", lines[0], lines[1])
	}
}

func TestLogFormatterSeverityTokens(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{"ERROR something broke", sgrRed},
		{"fatal: cannot open state", sgrRed},
		{"WARN disk almost full", sgrYellow},
		{"Warning: deprecated flag", sgrYellow},
		{"all quiet on this line", sgrDim},
	}
	for _, c := range cases {
		got := formatLines(t, []string{c.line}, "", 2)[0]
		if !strings.HasPrefix(got, c.want+logGutterGlyph) {
			t.Fatalf("%q got the gutter %q", c.line, got)
		}
	}
}

func TestLogFormatterSeverityLogfmtAndJSON(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{`time=now level=warn msg="disk filling up"`, sgrYellow},
		{`{"level":"error","msg":"boom"}`, sgrRed},
		{`{"level": "FATAL", "msg": "gone"}`, sgrRed},
		// An explicit level decides alone, a line about errors is not one.
		{`level=info msg="error rate low"`, sgrDim},
		// The severity is read behind the compose prefix.
		{"api-1  | level=error msg=down", sgrRed},
	}
	for _, c := range cases {
		got := formatLines(t, []string{c.line}, "", 2)[0]
		if !strings.HasPrefix(got, c.want+logGutterGlyph) {
			t.Fatalf("%q got the gutter %q", c.line, got)
		}
	}
}

func TestLogFormatterNoPrefix(t *testing.T) {
	got := formatLines(t, []string{"plain container output"}, "", 2)[0]
	if serviceTint(got) != "" {
		t.Fatalf("a line without a compose prefix got a tint: %q", got)
	}
	if plainLine(got) != logGutterGlyph+" plain container output" {
		t.Fatalf("the text moved: %q", plainLine(got))
	}
}

func TestLogFormatterGrepContextAndHighlight(t *testing.T) {
	input := []string{
		"alpha",
		"beta",
		"gamma boom delta",
		"epsilon",
		"zeta",
		"eta",
		"theta BOOM",
		"iota",
	}
	lines := formatLines(t, input, "bo+m", 1)
	want := []string{
		"▍ beta",
		"▍ gamma boom delta",
		"▍ epsilon",
		"--",
		"▍ eta",
		"▍ theta BOOM",
		"▍ iota",
	}
	if len(lines) != len(want) {
		t.Fatalf("grep passed %d lines: %q", len(lines), lines)
	}
	for i, line := range lines {
		if plainLine(line) != want[i] {
			t.Fatalf("line %d is %q, wanted %q", i, plainLine(line), want[i])
		}
	}
	if !strings.Contains(lines[1], sgrInverse+"boom"+sgrPlain) {
		t.Fatalf("the match is not inverted: %q", lines[1])
	}
	if !strings.Contains(lines[5], sgrInverse+"BOOM"+sgrPlain) {
		t.Fatalf("the match is not case insensitive: %q", lines[5])
	}
	if strings.Contains(lines[0], sgrInverse) {
		t.Fatalf("a context line is highlighted: %q", lines[0])
	}
}

func TestLogFormatterGrepRefusesBrokenPattern(t *testing.T) {
	if err := FormatLogs(strings.NewReader("x\n"), io.Discard, "(", 2); err == nil {
		t.Fatal("a broken pattern went through")
	}
}

func TestLogFormatterPassthrough(t *testing.T) {
	input := []string{"one", "", "three|no space means no prefix", "four"}
	lines := formatLines(t, input, "", 2)
	if len(lines) != len(input) {
		t.Fatalf("passthrough answered %d lines: %q", len(lines), lines)
	}
	for i, line := range lines {
		if plainLine(line) != logGutterGlyph+" "+input[i] {
			t.Fatalf("line %d is %q, wanted %q", i, plainLine(line), input[i])
		}
	}
}
