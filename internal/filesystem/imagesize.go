package filesystem

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"image"
	"io"
	"os"

	// The decoders the standard library brings, registered for their headers
	// alone: DecodeConfig reads the size out of the first bytes of a file and
	// never decodes the pixels.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

// ImageSize is the pixel size an image file is drawn at, 0 and 0 when it
// cannot be read: a format no decoder here knows (webp, avif, svg), a file that
// is not an image after all, or one that is truncated. A caller uses it to
// reserve the space a picture will take before the browser has it, so a zero
// means "say nothing" rather than an error: a page that cannot name the size
// still renders, it only shifts the way it always did.
//
// Drawn at, not stored as: a picture out of a phone often sits sideways in the
// file with an Exif tag that says how far to turn it, and a browser turns it
// before it draws it. Its two sides are then swapped against the frame header,
// and a caller that reserved the header's box would hold open a box the
// picture never fills.
func ImageSize(path string) (width, height int) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer func() { _ = file.Close() }()
	config, format, err := image.DecodeConfig(file)
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return 0, 0
	}
	if format == "jpeg" && turnsSideways(exifOrientation(jpegExif(file))) {
		return config.Height, config.Width
	}
	return config.Width, config.Height
}

// The Exif orientation values that put the picture on its side. The other four
// are upright or mirrored, and neither changes which side is the longer one.
func turnsSideways(orientation int) bool { return orientation >= 5 && orientation <= 8 }

// jpegExif is the Tiff block of the first Exif segment of a JPEG, nil when the
// file carries none. It walks the file segment by segment and stops at the
// start of the scan, so it never reads into the compressed image data: what
// comes after that point is pixels, and a byte in them that looks like a
// marker is not one.
func jpegExif(file io.ReadSeeker) []byte {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil
	}
	const (
		pad             = 0xFF
		startOfImage    = 0xD8
		app1            = 0xE1
		startOfScan     = 0xDA
		endOfImage      = 0xD9
		firstRestart    = 0xD0
		lastRestart     = 0xD7
		temporaryMarker = 0x01
	)
	reader := bufio.NewReader(file)
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil || header[0] != pad || header[1] != startOfImage {
		return nil
	}
	for {
		marker, err := reader.ReadByte()
		if err != nil {
			return nil
		}
		if marker != pad {
			return nil
		}
		for marker == pad {
			if marker, err = reader.ReadByte(); err != nil {
				return nil
			}
		}
		if marker == endOfImage || marker == startOfScan {
			return nil
		}
		if marker == temporaryMarker || (marker >= firstRestart && marker <= lastRestart) {
			continue
		}
		size := make([]byte, 2)
		if _, err := io.ReadFull(reader, size); err != nil {
			return nil
		}
		length := int(binary.BigEndian.Uint16(size))
		if length < 2 {
			return nil
		}
		segment := make([]byte, length-2)
		if _, err := io.ReadFull(reader, segment); err != nil {
			return nil
		}
		if prefix := []byte("Exif\x00\x00"); marker == app1 && bytes.HasPrefix(segment, prefix) {
			return segment[len(prefix):]
		}
	}
}

// exifOrientation reads the orientation tag out of a Tiff block, 0 when the
// block is not one, does not carry the tag, or is cut short. Only the first
// directory is read: that is where a camera writes the tag, the ones after it
// describe the thumbnail.
func exifOrientation(tiff []byte) int {
	const (
		tiffHeader     = 8
		tiffMagic      = 42
		entrySize      = 12
		tagOrientation = 0x0112
		typeShort      = 3
	)
	if len(tiff) < tiffHeader {
		return 0
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0
	}
	if order.Uint16(tiff[2:4]) != tiffMagic {
		return 0
	}
	directory := int(order.Uint32(tiff[4:8]))
	if directory < tiffHeader || directory+2 > len(tiff) {
		return 0
	}
	entries := int(order.Uint16(tiff[directory : directory+2]))
	for i := range entries {
		entry := directory + 2 + i*entrySize
		if entry+entrySize > len(tiff) {
			return 0
		}
		if order.Uint16(tiff[entry:entry+2]) != tagOrientation {
			continue
		}
		if order.Uint16(tiff[entry+2:entry+4]) != typeShort {
			return 0
		}
		return int(order.Uint16(tiff[entry+8 : entry+10]))
	}
	return 0
}
