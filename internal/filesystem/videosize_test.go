package filesystem

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func box(kind string, payload ...[]byte) []byte {
	body := bytes.Join(payload, nil)
	out := binary.BigEndian.AppendUint32(nil, uint32(8+len(body)))
	return append(append(out, kind...), body...)
}

var (
	upright     = [9]int32{0x10000, 0, 0, 0, 0x10000, 0, 0, 0, 0x40000000}
	quarterTurn = [9]int32{0, 0x10000, 0, -0x10000, 0, 0, 0, 0, 0x40000000}
)

func trackHeader(version byte, matrix [9]int32, width, height uint32) []byte {
	b := []byte{version, 0, 0, 7}
	if version == 1 {
		b = append(b, make([]byte, 32)...)
	} else {
		b = append(b, make([]byte, 20)...)
	}
	b = append(b, make([]byte, 16)...)
	for _, m := range matrix {
		b = binary.BigEndian.AppendUint32(b, uint32(m))
	}
	b = binary.BigEndian.AppendUint32(b, width<<16)
	b = binary.BigEndian.AppendUint32(b, height<<16)
	return box("tkhd", b)
}

func track(header []byte, handler string) []byte {
	hdlr := box("hdlr", make([]byte, 8), []byte(handler), make([]byte, 12))
	return box("trak", header, box("mdia", box("mdhd", make([]byte, 24)), hdlr))
}

func ebml(id uint32, payload ...[]byte) []byte {
	body := bytes.Join(payload, nil)
	var out []byte
	for shift := 24; shift >= 0; shift -= 8 {
		if c := byte(id >> shift); c != 0 || len(out) > 0 {
			out = append(out, c)
		}
	}
	n := len(body)
	out = append(out, 0x10|byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	return append(out, body...)
}

func ebmlNumber(id uint32, value uint64) []byte {
	var b []byte
	for v := value; v > 0 || len(b) == 0; v >>= 8 {
		b = append([]byte{byte(v)}, b...)
	}
	return ebml(id, b)
}

func unknownSize(id uint32, payload ...[]byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, id)
	out = append(out, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF)
	return append(out, bytes.Join(payload, nil)...)
}

func webmHeader() []byte {
	return ebml(ebmlHeaderID, ebml(0x4282, []byte("webm")))
}

func audioEntry() []byte {
	return ebml(trackEntryID, ebmlNumber(trackTypeID, 2), ebml(0xE1, ebmlNumber(0xB5, 48000)))
}

func videoEntry(settings ...[]byte) []byte {
	return ebml(trackEntryID, ebmlNumber(0xD7, 1), ebmlNumber(trackTypeID, videoTrackType), ebml(videoID, settings...))
}

func writeVideo(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

type videoCase struct {
	name          string
	data          []byte
	width, height int
}

func videoCases() []videoCase {
	media := box("mdat", make([]byte, 4096))
	ftyp := box("ftyp", []byte("isom"), make([]byte, 4), []byte("isomiso2mp41"))
	quicktime := box("ftyp", []byte("qt  "), make([]byte, 4), []byte("qt  "))
	largeMedia := append(binary.BigEndian.AppendUint32(nil, 1), "mdat"...)
	largeMedia = binary.BigEndian.AppendUint64(largeMedia, 16+1024)
	largeMedia = append(largeMedia, make([]byte, 1024)...)
	return []videoCase{
		{"recorder.mp4", bytes.Join([][]byte{ftyp, box("moov", box("mvhd", make([]byte, 100)), track(trackHeader(1, upright, 640, 360), "vide"))}, nil), 640, 360},
		{"index-at-end.mp4", bytes.Join([][]byte{ftyp, media, box("moov", track(trackHeader(0, upright, 0, 0), "soun"), track(trackHeader(0, upright, 1280, 720), "vide"))}, nil), 1280, 720},
		{"upright-phone.mov", bytes.Join([][]byte{quicktime, box("wide"), media, box("moov", track(trackHeader(0, quarterTurn, 1920, 1080), "vide"))}, nil), 1080, 1920},
		{"chapters.mov", bytes.Join([][]byte{quicktime, box("moov", track(trackHeader(0, upright, 320, 20), "text"), track(trackHeader(0, upright, 678, 154), "vide"))}, nil), 678, 154},
		{"large-media.m4v", bytes.Join([][]byte{ftyp, largeMedia, box("moov", track(trackHeader(0, upright, 400, 400), "vide"))}, nil), 400, 400},
		{"sound-only.mp4", bytes.Join([][]byte{ftyp, box("moov", track(trackHeader(0, upright, 0, 0), "soun"))}, nil), 0, 0},
		{"truncated.mp4", bytes.Join([][]byte{ftyp, box("moov", track(trackHeader(0, upright, 640, 360), "vide"))}, nil)[:len(ftyp)+60], 0, 0},
		{"recorder.webm", bytes.Join([][]byte{webmHeader(), unknownSize(segmentID, ebml(0x1549A966, ebmlNumber(0x2AD7B1, 1000000)), ebml(tracksID, audioEntry(), videoEntry(ebmlNumber(pixelWidthID, 360), ebmlNumber(pixelHeightID, 640))), unknownSize(clusterID, make([]byte, 64)))}, nil), 360, 640},
		{"anamorphic.mkv", bytes.Join([][]byte{webmHeader(), ebml(segmentID, ebml(tracksID, videoEntry(ebmlNumber(pixelWidthID, 720), ebmlNumber(pixelHeightID, 576), ebmlNumber(displayWidthID, 1024), ebmlNumber(displayHeightID, 576))))}, nil), 1024, 576},
		{"shape-only.webm", bytes.Join([][]byte{webmHeader(), ebml(segmentID, ebml(tracksID, videoEntry(ebmlNumber(pixelWidthID, 720), ebmlNumber(pixelHeightID, 576), ebmlNumber(displayWidthID, 16), ebmlNumber(displayHeightID, 9), ebmlNumber(displayUnitID, 3))))}, nil), 1024, 576},
		{"tracks-after-media.webm", bytes.Join([][]byte{webmHeader(), unknownSize(segmentID, unknownSize(clusterID, make([]byte, 64)))}, nil), 0, 0},
		{"picture.png", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), 0, 0},
		{"empty.mp4", nil, 0, 0},
	}
}

func endlessBoxes() []byte {
	var data []byte
	for i := 0; i < 10000; i++ {
		data = append(data, box("free")...)
	}
	return append(data, box("moov", track(trackHeader(0, upright, 640, 360), "vide"))...)
}

// The size has to come out of the files the browsers and phones write, and
// nothing out of anything else: a wrong size reserves a box the clip never
// fills, which is worse than none.
func TestVideoSize(t *testing.T) {
	for _, c := range videoCases() {
		t.Run(c.name, func(t *testing.T) {
			width, height := VideoSize(writeVideo(t, c.name, c.data))
			if width != c.width || height != c.height {
				t.Fatalf("VideoSize = %dx%d, want %dx%d", width, height, c.width, c.height)
			}
		})
	}
	if width, height := VideoSize(filepath.Join(t.TempDir(), "missing.mp4")); width != 0 || height != 0 {
		t.Fatalf("a missing file has the size %dx%d", width, height)
	}
}

// A file of nothing but tiny boxes ends the walk early instead of reading
// every one of them while a page renders.
func TestVideoSizeStopsOnEndlessBoxes(t *testing.T) {
	if width, height := VideoSize(writeVideo(t, "boxes.mp4", endlessBoxes())); width != 0 || height != 0 {
		t.Fatalf("VideoSize = %dx%d, want nothing", width, height)
	}
}

// Every file a page shows is read while the page renders, a crafted one
// included, so no input may panic or hand the page a box no layout can hold.
func FuzzVideoSize(f *testing.F) {
	for _, c := range videoCases() {
		f.Add(c.data)
	}
	f.Add(endlessBoxes())
	f.Fuzz(func(t *testing.T, data []byte) {
		width, height := VideoSize(writeVideo(t, "clip", data))
		if width < 0 || height < 0 || width > maxSide || height > maxSide {
			t.Fatalf("VideoSize = %dx%d", width, height)
		}
	})
}
