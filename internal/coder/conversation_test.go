package coder

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func text(id, role, words string) Message {
	return Message{ID: id, Role: role, Parts: []Part{{Kind: PartText, Text: words}}}
}

func TestTheWindowKeepsTheNewestMessagesWholeAndCountsTheRest(t *testing.T) {
	all := []Message{text("1", RoleUser, "one"), text("2", RoleCoder, "two"), text("3", RoleUser, "three"), text("4", RoleCoder, "four")}
	kept, dropped := Window(all, 2, 0)
	if dropped != 2 || len(kept) != 2 || kept[0].Text() != "three" || kept[1].Text() != "four" {
		t.Fatalf("kept %d (dropped %d): %+v", len(kept), dropped, kept)
	}
	kept, dropped = Window(all, 0, 0)
	if dropped != 0 || len(kept) != 4 {
		t.Fatalf("without a bound every message stands: kept %d, dropped %d", len(kept), dropped)
	}
}

func TestTheCapDropsOffTheTopButNeverTheNewestMessage(t *testing.T) {
	long := strings.Repeat("x", 100)
	all := []Message{text("1", RoleUser, long), text("2", RoleCoder, "short"), text("3", RoleUser, long)}
	kept, dropped := Window(all, 0, 50)
	if len(kept) != 1 || dropped != 2 || kept[0].Text() != long {
		t.Fatalf("a newest message over the cap still stands alone: kept %d, dropped %d", len(kept), dropped)
	}
	kept, dropped = Window(all, 0, 120)
	if len(kept) != 2 || dropped != 1 || kept[0].Text() != "short" {
		t.Fatalf("the cap counts the recorded text: kept %d, dropped %d", len(kept), dropped)
	}
}

func TestAMessageJoinsItsTextPartsAndLeavesTheToolsOut(t *testing.T) {
	m := Message{Role: RoleCoder, Parts: []Part{
		{Kind: PartText, Text: "first"},
		{Kind: PartTool, Tool: "Bash", Text: "ls"},
		{Kind: PartText, Text: "second"},
	}}
	if got := m.Text(); got != "first\n\nsecond" {
		t.Fatalf("text: %q", got)
	}
}

// A coder turn runs from the words that started it to the user's next ones,
// and a message the record names nothing for gets the same ordinal on every
// reading, so a page can ask for what stands before it.
func TestTheBuilderMergesATurnAndNamesEveryMessage(t *testing.T) {
	read := func() []Message {
		var b TurnBuilder
		b.Add(text("", RoleUser, "hi"), "", timeZero)
		b.AddPart("c1", timeZero, Part{Kind: PartTool, Tool: "Bash", Text: "ls"})
		b.AddPart("c2", timeZero, Part{Kind: PartText, Text: "done"})
		b.CloseTurn()
		b.AddPart("c3", timeZero, Part{Kind: PartText, Text: "more"})
		b.Add(text("", RoleUser, "thanks"), "u2", timeZero)
		return b.Messages()
	}
	all := read()
	if len(all) != 4 || all[1].ID != "c1" || len(all[1].Parts) != 2 || all[2].ID != "c3" || all[3].ID != "u2" {
		t.Fatalf("messages: %+v", all)
	}
	if again := read(); again[0].ID == "" || again[0].ID != all[0].ID {
		t.Fatalf("an unnamed message changed its id between readings: %q, %q", all[0].ID, again[0].ID)
	}
}

// Manager.Conversation answers the newest page and, with before, the page
// above a message.
func TestAPageBeforeAMessageIsWhatStandsAboveIt(t *testing.T) {
	all := []Message{text("a", RoleUser, "1"), text("b", RoleCoder, "2"), text("c", RoleUser, "3"), text("d", RoleCoder, "4")}
	m := &Manager{coder: conversationCoder{all: all}}
	newest, err := m.Conversation("11111111-1111-4111-8111-111111111111", 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(newest.Messages) != 2 || newest.Messages[0].ID != "c" || newest.Dropped != 2 {
		t.Fatalf("newest page: %+v", newest)
	}
	older, err := m.Conversation("11111111-1111-4111-8111-111111111111", 2, "c")
	if err != nil {
		t.Fatal(err)
	}
	if len(older.Messages) != 2 || older.Messages[0].ID != "a" || older.Dropped != 0 {
		t.Fatalf("page before c: %+v", older)
	}
	// A record rewritten under the view, a compaction or a /clear, no longer
	// holds the message the view asks about: the newest page again would
	// stand above what the view shows, the conversation twice.
	if _, err := m.Conversation("11111111-1111-4111-8111-111111111111", 2, "gone"); !errors.Is(err, ErrConversationChanged) {
		t.Fatalf("an unknown before: err = %v, want ErrConversationChanged", err)
	}
}

var timeZero time.Time

// conversationCoder is a coder whose record holds the given messages.
type conversationCoder struct {
	Coder
	all []Message
}

func (c conversationCoder) SessionConversation(string) ([]Message, error) { return c.all, nil }
