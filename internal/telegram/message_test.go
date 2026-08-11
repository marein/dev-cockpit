package telegram

import (
	"strings"
	"testing"
)

func TestAShortAnswerStaysOneMessage(t *testing.T) {
	parts := splitMessage("one line")
	if len(parts) != 1 || parts[0] != "one line" {
		t.Fatalf("a short answer was cut: %q", parts)
	}
}

func TestALongAnswerIsCutAtParagraphs(t *testing.T) {
	first := strings.Repeat("a", 2000)
	second := strings.Repeat("b", 2000)
	parts := splitMessage(first + "\n\n" + second)
	if len(parts) != 2 {
		t.Fatalf("got %d parts, want 2", len(parts))
	}
	if !strings.HasPrefix(parts[0], "(1/2)\n") || !strings.HasPrefix(parts[1], "(2/2)\n") {
		t.Fatalf("the parts are not counted: %q", []string{parts[0][:8], parts[1][:8]})
	}
	if !strings.HasSuffix(parts[0], "a") || !strings.HasPrefix(strings.TrimPrefix(parts[1], "(2/2)\n"), "b") {
		t.Fatal("the cut did not fall on the paragraph boundary")
	}
}

func TestALongAnswerIsCutAtLinesWhenThereIsNoParagraph(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString(strings.Repeat("x", 30))
		b.WriteString("\n")
	}
	parts := splitMessage(b.String())
	if len(parts) < 2 {
		t.Fatalf("got %d parts, want more than one", len(parts))
	}
	for _, part := range parts {
		body := part[strings.IndexByte(part, '\n')+1:]
		for _, line := range strings.Split(body, "\n") {
			if len(line) != 30 {
				t.Fatalf("a line was torn in half: %q", line)
			}
		}
	}
}

func TestOneLongWordIsCutHard(t *testing.T) {
	parts := splitMessage(strings.Repeat("z", maxMessageRunes*2+10))
	if len(parts) != 3 {
		t.Fatalf("got %d parts, want 3", len(parts))
	}
	for _, part := range parts {
		if len([]rune(part)) > maxMessageRunes+len("(1/3)\n") {
			t.Fatalf("a part is %d runes long", len([]rune(part)))
		}
	}
}

func TestEveryPartStaysWithinWhatTelegramTakes(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 60; i++ {
		b.WriteString(strings.Repeat("word ", 40))
		b.WriteString("\n\n")
	}
	for _, part := range splitMessage(b.String()) {
		if len([]rune(part)) > 4096 {
			t.Fatalf("a part is %d runes, Telegram takes 4096", len([]rune(part)))
		}
	}
}

func TestRunesSurviveTheCut(t *testing.T) {
	text := strings.Repeat("ümlaut ", 1200)
	joined := ""
	for _, part := range splitMessage(text) {
		joined += strings.TrimSpace(part[strings.IndexByte(part, '\n')+1:])
	}
	if !strings.Contains(joined, "ümlaut") || strings.Contains(joined, "�") {
		t.Fatal("the cut broke a multi byte rune")
	}
}
