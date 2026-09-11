package filesystem

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func imageFile(t *testing.T, dir, name string, width, height int) string {
	t.Helper()
	src := image.NewRGBA(image.Rect(0, 0, width, height))
	src.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	var buf bytes.Buffer
	var err error
	switch filepath.Ext(name) {
	case ".png":
		err = png.Encode(&buf, src)
	case ".jpg":
		err = jpeg.Encode(&buf, src, nil)
	case ".gif":
		err = gif.Encode(&buf, src, nil)
	}
	if err != nil {
		t.Fatalf("encode %s: %v", name, err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// The three formats a screenshot or a photo actually arrives in have to answer
// with their real size, because that size is what reserves the space.
func TestImageSizeReadsTheFormatsTheStandardLibraryKnows(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"shot.png", "photo.jpg", "clip.gif"} {
		width, height := ImageSize(imageFile(t, dir, name, 640, 400))
		if width != 640 || height != 400 {
			t.Fatalf("%s reads as %dx%d, want 640x400", name, width, height)
		}
	}
}

// Everything else answers with nothing instead of an error: the page renders
// without the size the way it always did.
func TestImageSizeStaysQuietOnWhatItCannotRead(t *testing.T) {
	dir := t.TempDir()
	notes := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(notes, []byte("no picture in here"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	truncated := filepath.Join(dir, "half.png")
	whole, err := os.ReadFile(imageFile(t, dir, "whole.png", 20, 10))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := os.WriteFile(truncated, whole[:4], 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	for _, path := range []string{notes, truncated, filepath.Join(dir, "gone.png")} {
		if width, height := ImageSize(path); width != 0 || height != 0 {
			t.Fatalf("%s reads as %dx%d, want nothing", path, width, height)
		}
	}
}

// exifJPEG is the same picture with an Exif segment spliced in behind the
// start of image marker, the place a camera writes it.
func exifJPEG(t *testing.T, dir, name string, width, height, orientation int) string {
	t.Helper()
	whole, err := os.ReadFile(imageFile(t, dir, "plain-"+name, width, height))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	tiff := []byte{'M', 'M', 0, 42, 0, 0, 0, 8}
	tiff = append(tiff, 0, 1) // one entry
	tiff = append(tiff, 0x01, 0x12, 0, 3, 0, 0, 0, 1, byte(orientation>>8), byte(orientation), 0, 0)
	tiff = append(tiff, 0, 0, 0, 0) // no directory after this one
	body := append([]byte("Exif\x00\x00"), tiff...)
	segment := []byte{0xFF, 0xE1, byte((len(body) + 2) >> 8), byte(len(body) + 2)}
	segment = append(segment, body...)
	with := append([]byte{}, whole[:2]...)
	with = append(with, segment...)
	with = append(with, whole[2:]...)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, with, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// A picture that lies sideways in the file answers with the size it is drawn
// at, because that is the box the page has to hold open and the ratio it has
// to keep. The four upright and mirrored orientations answer with the size as
// it is stored, and so does a file without the tag.
func TestImageSizeFollowsTheExifOrientation(t *testing.T) {
	dir := t.TempDir()
	for orientation, want := range map[int][2]int{
		1: {640, 400}, 2: {640, 400}, 3: {640, 400}, 4: {640, 400},
		5: {400, 640}, 6: {400, 640}, 7: {400, 640}, 8: {400, 640},
	} {
		name := "photo" + string(rune('0'+orientation)) + ".jpg"
		width, height := ImageSize(exifJPEG(t, dir, name, 640, 400, orientation))
		if width != want[0] || height != want[1] {
			t.Fatalf("orientation %d reads as %dx%d, want %dx%d", orientation, width, height, want[0], want[1])
		}
	}
	if width, height := ImageSize(imageFile(t, dir, "bare.jpg", 640, 400)); width != 640 || height != 400 {
		t.Fatalf("a jpeg without the tag reads as %dx%d, want 640x400", width, height)
	}
}

// A broken Exif segment is not a broken picture: the size out of the frame
// header still stands, and nothing here reads past the end of the block.
func TestImageSizeSurvivesABrokenExifSegment(t *testing.T) {
	dir := t.TempDir()
	whole, err := os.ReadFile(exifJPEG(t, dir, "photo.jpg", 640, 400, 6))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, cut := range []int{6, 10, 14, 20, 24} {
		broken := append([]byte{}, whole[:2]...)
		broken = append(broken, whole[2:2+cut]...)
		broken = append(broken, whole[2+cut+4:]...)
		path := filepath.Join(dir, "broken.jpg")
		if err := os.WriteFile(path, broken, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		width, height := ImageSize(path)
		if (width != 640 || height != 400) && (width != 400 || height != 640) && (width != 0 || height != 0) {
			t.Fatalf("cut %d reads as %dx%d", cut, width, height)
		}
	}
}
