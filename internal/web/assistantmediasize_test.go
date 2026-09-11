package web

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/assistant"
)

func writeShot(t *testing.T, path string, width, height int) {
	t.Helper()
	src := image.NewRGBA(image.Rect(0, 0, width, height))
	src.Set(0, 0, color.RGBA{R: 9, G: 9, B: 9, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// Both ways a picture reaches the transcript carry their pixel size: a file
// the user attached, and one an answer points at. The size is what holds the
// space open, so a conversation full of screenshots stops rearranging itself
// under the reader while the pictures arrive.
func TestAPictureInTheTranscriptCarriesItsSize(t *testing.T) {
	stateDir := t.TempDir()
	conversations, workspace, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	s := &Server{conversations: conversations, assistant: workspace}
	shot := filepath.Join(workspace.Workspace(), "assistant-files", "board.png")
	writeShot(t, shot, 1280, 800)

	view := s.assistantMessageView("c1", assistant.Message{
		ID:          "m1",
		Role:        assistant.RoleUser,
		Content:     "look at this",
		Attachments: []assistant.Attachment{{Name: "board.png", Path: shot, Media: "image", Size: 4096}},
	}, false, true, "Claude")
	if len(view.Attachments) != 1 {
		t.Fatalf("want one attachment, got %d", len(view.Attachments))
	}
	if view.Attachments[0].Width != 1280 || view.Attachments[0].Height != 800 {
		t.Fatalf("the attachment reads as %dx%d, want 1280x800", view.Attachments[0].Width, view.Attachments[0].Height)
	}

	html := string(s.assistantMarkdown("c1", "![the board](assistant-files/board.png)"))
	for _, want := range []string{`width="1280"`, `height="800"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("want %s in the answer, got %q", want, html)
		}
	}
}

// Everything that is not a picture is left alone: no file is opened for it,
// and nothing in the view pretends to know a box.
func TestAFileThatIsNoPictureCarriesNoSize(t *testing.T) {
	stateDir := t.TempDir()
	conversations, workspace, err := assistant.New(stateDir, oneCoder{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	s := &Server{conversations: conversations, assistant: workspace}
	notes := filepath.Join(workspace.Workspace(), "assistant-files", "notes.txt")
	writeShot(t, filepath.Join(workspace.Workspace(), "assistant-files", "keep.png"), 10, 10)
	if err := os.WriteFile(notes, []byte("no picture"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	view := s.assistantMessageView("c1", assistant.Message{
		ID:          "m1",
		Role:        assistant.RoleUser,
		Attachments: []assistant.Attachment{{Name: "notes.txt", Path: notes, Media: "file", Size: 10}},
	}, false, true, "Claude")
	if view.Attachments[0].Width != 0 || view.Attachments[0].Height != 0 {
		t.Fatalf("a text file came back with a box: %dx%d", view.Attachments[0].Width, view.Attachments[0].Height)
	}
}
