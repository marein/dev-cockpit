package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/assistant"
)

func TestAPickedMessageShowsItsNoteAndItsCard(t *testing.T) {
	stateDir := t.TempDir()
	conversations, workspace, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	s := &Server{assistants: conversations, workspace: workspace}
	const id = "11111111-1111-4111-8111-111111111111"
	uploads, err := conversations.UploadDir(id)
	if err != nil {
		t.Fatalf("upload dir: %v", err)
	}
	drawing := filepath.Join(uploads, "drawing.png")
	writeShot(t, drawing, 640, 400)
	other := filepath.Join(uploads, "notes.txt")
	if err := os.WriteFile(other, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	content := "Look <b>here</b>\n\n## Picked in the Browser\n\n- Selector: `#save`"
	view := s.assistantMessageView(id, assistant.Message{
		ID:      "m1",
		Role:    assistant.RoleUser,
		Content: content,
		Attachments: []assistant.Attachment{
			{Name: "drawing.png", Path: drawing, Media: "image"},
			{Name: "notes.txt", Path: other, Media: "file"},
		},
		Picked: &assistant.Picked{Note: "Look <b>here</b>", Sections: []assistant.PickedSection{{Kind: "image", Title: "1. Drawing", Picture: "drawing.png"}}},
	}, false, true, "Claude")
	if string(view.HTML) != "Look &lt;b&gt;here&lt;/b&gt;" {
		t.Fatalf("the bubble shows %q", view.HTML)
	}
	if view.Picked == nil || view.Picked.Copy != content {
		t.Fatalf("the card is %+v", view.Picked)
	}
	if shot, ok := view.Picked.Pictures["drawing.png"]; !ok || shot.URL != "/assistants/"+id+"/media/user-upload/drawing.png" || shot.Width != 640 {
		t.Fatalf("the card's picture is %+v", view.Picked.Pictures)
	}
	if len(view.Attachments) != 1 || view.Attachments[0].Name != "notes.txt" {
		t.Fatalf("under the message stand %+v", view.Attachments)
	}
	plain := s.assistantMessageView(id, assistant.Message{ID: "m2", Role: assistant.RoleUser, Content: content}, false, true, "Claude")
	if plain.Picked != nil || !strings.Contains(string(plain.HTML), "## Picked in the Browser") {
		t.Fatalf("a message without a card renders %q with %+v", plain.HTML, plain.Picked)
	}
}
