package telegram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/local/dev-cockpit/internal/assistant"
)

// photoUpdate is a picture the way Telegram sends one: several offered sizes,
// smallest first, with the caption as the message's text.
func photoUpdate(tc *testChannel, updateID int64, caption string) Update {
	return Update{
		UpdateID: updateID,
		Message: &Message{
			MessageID: updateID,
			Date:      tc.clock.Unix(),
			Chat:      Chat{ID: 42, Type: "private", FirstName: "marein"},
			Caption:   caption,
			Photo: []PhotoSize{
				{FileID: "small", Width: 90, Height: 60, FileSize: 900},
				{FileID: "large", Width: 1280, Height: 860, FileSize: 90000},
			},
		},
	}
}

func documentUpdate(tc *testChannel, updateID int64, doc *Document, caption string) Update {
	return Update{
		UpdateID: updateID,
		Message: &Message{
			MessageID: updateID,
			Date:      tc.clock.Unix(),
			Chat:      Chat{ID: 42, Type: "private", FirstName: "marein"},
			Caption:   caption,
			Document:  doc,
		},
	}
}

func TestAPhotoArrivesAsAnAttachment(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.api.push(photoUpdate(tc, 1, "what is wrong here"))
	tc.Start()

	waitFor(t, "the message with the picture", func() bool { return len(tc.convs.prompts()) == 1 })
	prompt := tc.convs.prompts()[0]
	if prompt.Text != "what is wrong here" {
		t.Fatalf("the caption did not become the message: %q", prompt.Text)
	}
	if len(prompt.Attachments) != 1 {
		t.Fatalf("got %d attachments, want the picture", len(prompt.Attachments))
	}
	attachment := prompt.Attachments[0]
	if attachment.Media != "image" {
		t.Fatalf("the attachment is %q, want an image", attachment.Media)
	}
	if !strings.HasPrefix(filepath.Base(attachment.Path), "telegram-") {
		t.Fatalf("the file kept a name from outside: %q", attachment.Path)
	}
	body, err := os.ReadFile(attachment.Path)
	if err != nil || string(body) != "image-bytes" {
		t.Fatalf("the picture was not stored: %v %q", err, body)
	}
}

func TestAPhotoWithoutACaptionGoesInWithoutText(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.api.push(photoUpdate(tc, 1, ""))
	tc.Start()

	waitFor(t, "the message with the picture", func() bool { return len(tc.convs.prompts()) == 1 })
	prompt := tc.convs.prompts()[0]
	if prompt.Text != "" || len(prompt.Attachments) != 1 {
		t.Fatalf("a picture without a caption arrived as %+v", prompt)
	}
}

func TestAPictureSentAsAFileGoesTheSameWay(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.api.push(documentUpdate(tc, 1, &Document{
		FileID:   "doc",
		FileName: "../../evil.sh",
		MimeType: "image/png",
		FileSize: 4096,
	}, "as a file"))
	tc.Start()

	waitFor(t, "the message with the picture", func() bool { return len(tc.convs.prompts()) == 1 })
	prompt := tc.convs.prompts()[0]
	if len(prompt.Attachments) != 1 || prompt.Attachments[0].Media != "image" {
		t.Fatalf("a picture sent as a file did not arrive as a picture: %+v", prompt)
	}
	name := filepath.Base(prompt.Attachments[0].Path)
	if !strings.HasPrefix(name, "telegram-") || !strings.HasSuffix(name, ".png") {
		t.Fatalf("the name came out of the answer instead of being built here: %q", name)
	}
	if filepath.Dir(prompt.Attachments[0].Path) != tc.convs.uploadDir {
		t.Fatalf("the file landed outside the conversation's directory: %q", prompt.Attachments[0].Path)
	}
}

func TestAFileOverTheDownloadLimitIsRefused(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.api.push(documentUpdate(tc, 1, &Document{
		FileID:   "doc",
		MimeType: "image/jpeg",
		FileSize: MaxDownloadBytes + 1,
	}, "look"))
	tc.Start()

	waitFor(t, "the refusal", func() bool { return len(tc.api.messages()) == 1 })
	if sent := tc.api.messages(); !strings.Contains(sent[0], "20 MB") {
		t.Fatalf("the refusal does not say what the limit is: %q", sent[0])
	}
	if prompts := tc.convs.prompts(); len(prompts) != 0 {
		t.Fatalf("something reached the conversation anyway: %+v", prompts)
	}
}

func TestAPictureTelegramReportsTooLateIsStillRefused(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	// The size only shows up in the getFile answer, which is where a picture
	// with no size on the message lands.
	tc.api.getFileSize = MaxDownloadBytes + 1
	tc.api.push(photoUpdate(tc, 1, ""))
	tc.Start()

	waitFor(t, "the refusal", func() bool { return len(tc.api.messages()) == 1 })
	if prompts := tc.convs.prompts(); len(prompts) != 0 {
		t.Fatalf("the oversized picture was downloaded anyway: %+v", prompts)
	}
}

func TestAVoiceMessageIsRefusedInsteadOfVanishing(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	// A voice message carries neither text nor a picture, like a video, a
	// sticker or a location.
	tc.api.push(Update{
		UpdateID: 1,
		Message: &Message{
			MessageID: 1,
			Date:      tc.clock.Unix(),
			Chat:      Chat{ID: 42, Type: "private"},
		},
	})
	tc.Start()

	waitFor(t, "the refusal", func() bool { return len(tc.api.messages()) == 1 })
	sent := tc.api.messages()
	if !strings.Contains(sent[0], "text and images") {
		t.Fatalf("the refusal does not say what arrives here: %q", sent[0])
	}
	if prompts := tc.convs.prompts(); len(prompts) != 0 {
		t.Fatalf("something reached the conversation: %+v", prompts)
	}
}

func TestAFileThatIsNotAPictureIsRefused(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.api.push(documentUpdate(tc, 1, &Document{
		FileID:   "doc",
		FileName: "notes.pdf",
		MimeType: "application/pdf",
		FileSize: 1024,
	}, "read this"))
	tc.Start()

	waitFor(t, "the refusal", func() bool { return len(tc.api.messages()) == 1 })
	if prompts := tc.convs.prompts(); len(prompts) != 0 {
		t.Fatalf("a pdf reached the conversation: %+v", prompts)
	}
}

func TestTheLargestOfferedSizeIsTaken(t *testing.T) {
	update := photoUpdate(&testChannel{}, 1, "")
	file, kind := pickMedia(update.Message)
	if kind != mediaImage || file.ID != "large" {
		t.Fatalf("pickMedia took %q (%v)", file.ID, kind)
	}
}

func TestAFullQueueAnswersInTheChat(t *testing.T) {
	tc := newTestChannel(t)
	tc.pair(42)
	tc.convs.sendErr = errTooManyWaiting
	tc.api.push(tc.message(1, 42, "one more"))
	tc.Start()

	waitFor(t, "the answer", func() bool { return len(tc.api.messages()) == 1 })
	if sent := tc.api.messages(); sent[0] != errTooManyWaiting.Error() {
		t.Fatalf("the refusal of the service was rewritten: %q", sent[0])
	}
}

var errTooManyWaiting = assistant.ErrBusy
