package filesystem

import (
	"encoding/binary"
	"io"
	"math/bits"
	"os"
)

// VideoSize is the size a video file is shown at, 0 and 0 when it cannot be
// read: a container nobody here walks (ogg), a file without a video track, or
// one that is truncated or broken. Like ImageSize it only reserves the space a
// clip will take, so a zero means "say nothing".
//
// MP4 and QuickTime carry the size in the header of the video track, turned by
// the track's matrix: a phone that filmed upright stores the frames sideways
// and a quarter turn, and a player shows them turned. WebM and Matroska carry
// it in the video settings of the track entry, as display size where one is
// given.
//
// It is hand written and not a library: no Go library reads MP4 and QuickTime
// together with WebM and Matroska, and the candidates (abema/go-mp4,
// Eyevinn/mp4ff, at-wat/ebml-go) are large or allocate a lot on a crafted
// file, up to 188 MB for a WebM with a 4 MB cluster before its tracks.
func VideoSize(path string) (width, height int) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return 0, 0
	}
	var magic [4]byte
	if _, err := file.ReadAt(magic[:], 0); err != nil {
		return 0, 0
	}
	if binary.BigEndian.Uint32(magic[:]) == ebmlHeaderID {
		width, height = matroskaSize(file, info.Size())
	} else {
		width, height = isoSize(file, info.Size())
	}
	if width <= 0 || height <= 0 || width > maxSide || height > maxSide {
		return 0, 0
	}
	return width, height
}

// maxSide is far beyond any real clip and keeps a broken header from
// handing the page a box no layout can hold.
const maxSide = 1 << 16

// A walk looks at this many elements of one parent at most. The size sits in
// the first few of them in every file a recorder writes, and a file made of
// nothing but tiny elements must not keep a page render reading.
const maxChildren = 64

// isoChildren walks the boxes of an MP4 or QuickTime file between from and to.
// visit gets each box's type and payload and answers whether to go on.
func isoChildren(r io.ReaderAt, from, to int64, visit func(kind string, from, to int64) bool) {
	pos := from
	for n := 0; n < maxChildren && pos+8 <= to; n++ {
		var head [16]byte
		if _, err := r.ReadAt(head[:8], pos); err != nil {
			return
		}
		size := int64(binary.BigEndian.Uint32(head[:4]))
		body := pos + 8
		switch size {
		case 0:
			size = to - pos
		case 1:
			if _, err := r.ReadAt(head[8:], pos+8); err != nil {
				return
			}
			size = int64(binary.BigEndian.Uint64(head[8:]))
			body = pos + 16
		}
		if size < body-pos || size > to-pos {
			return
		}
		if !visit(string(head[4:8]), body, pos+size) {
			return
		}
		pos += size
	}
}

func isoSize(r io.ReaderAt, end int64) (width, height int) {
	isoChildren(r, 0, end, func(kind string, from, to int64) bool {
		if kind != "moov" {
			return true
		}
		isoChildren(r, from, to, func(kind string, from, to int64) bool {
			if kind != "trak" {
				return true
			}
			w, h, video := isoTrack(r, from, to)
			if video && w > 0 && h > 0 {
				width, height = w, h
				return false
			}
			return true
		})
		return false
	})
	return width, height
}

// isoTrack reads a track's shown size from its header and whether its handler
// names it a video track: a QuickTime chapter or subtitle track carries a size
// as well.
func isoTrack(r io.ReaderAt, from, to int64) (width, height int, video bool) {
	isoChildren(r, from, to, func(kind string, from, to int64) bool {
		switch kind {
		case "tkhd":
			width, height = trackHeaderSize(r, from, to)
		case "mdia":
			isoChildren(r, from, to, func(kind string, from, to int64) bool {
				if kind != "hdlr" {
					return true
				}
				var handler [12]byte
				video = to-from >= 12 && readFull(r, handler[:], from) && string(handler[8:]) == "vide"
				return false
			})
		}
		return true
	})
	return width, height, video
}

// trackHeaderSize is the 16.16 fixed point width and height at the end of a
// track header, swapped when the matrix before them turns the picture a
// quarter: its a and d entries are zero then.
func trackHeaderSize(r io.ReaderAt, from, to int64) (int, int) {
	var head [96]byte
	at := 76
	if to-from > 0 && readFull(r, head[:1], from) && head[0] == 1 {
		at = 88
	}
	if to-from < int64(at+8) || !readFull(r, head[:at+8], from) {
		return 0, 0
	}
	matrix := head[at-36 : at]
	width := int(binary.BigEndian.Uint32(head[at:]) >> 16)
	height := int(binary.BigEndian.Uint32(head[at+4:]) >> 16)
	if binary.BigEndian.Uint32(matrix[0:]) == 0 && binary.BigEndian.Uint32(matrix[16:]) == 0 {
		return height, width
	}
	return width, height
}

// The Matroska element ids on the way to a video track's size.
const (
	ebmlHeaderID    = 0x1A45DFA3
	segmentID       = 0x18538067
	tracksID        = 0x1654AE6B
	clusterID       = 0x1F43B675
	trackEntryID    = 0xAE
	trackTypeID     = 0x83
	videoID         = 0xE0
	pixelWidthID    = 0xB0
	pixelHeightID   = 0xBA
	displayWidthID  = 0x54B0
	displayHeightID = 0x54BA
	displayUnitID   = 0x54B2
	videoTrackType  = 1
)

// ebmlChildren walks the elements of a Matroska file between from and to. An
// element of unknown size (a live recording's segment) runs to the end of its
// parent, and nothing after it can be found.
func ebmlChildren(r io.ReaderAt, from, to int64, visit func(id uint32, from, to int64) bool) {
	pos := from
	for n := 0; n < maxChildren && pos < to; n++ {
		var head [12]byte
		got, _ := r.ReadAt(head[:min(int64(len(head)), to-pos)], pos)
		id, idLen, _ := ebmlVint(head[:got], true)
		size, sizeLen, unknown := ebmlVint(head[idLen:got], false)
		if idLen == 0 || sizeLen == 0 {
			return
		}
		body := pos + int64(idLen+sizeLen)
		end := to
		if !unknown {
			if size > uint64(to-body) {
				return
			}
			end = body + int64(size)
		}
		if !visit(uint32(id), body, end) || unknown {
			return
		}
		pos = end
	}
}

// ebmlVint reads one variable length integer: the leading zeros of its first
// byte give its length. An id keeps its length marker, a size drops it, and a
// size of all ones is unknown.
func ebmlVint(b []byte, id bool) (value uint64, length int, unknown bool) {
	if len(b) == 0 || b[0] == 0 {
		return 0, 0, false
	}
	length = bits.LeadingZeros8(b[0]) + 1
	if length > len(b) || (id && length > 4) {
		return 0, 0, false
	}
	mask := byte(0xFF) >> length
	value = uint64(b[0] & mask)
	if id {
		value = uint64(b[0])
	}
	unknown = b[0]&mask == mask
	for _, c := range b[1:length] {
		value = value<<8 | uint64(c)
		unknown = unknown && c == 0xFF
	}
	return value, length, unknown && !id
}

func ebmlUint(r io.ReaderAt, from, to int64) uint64 {
	var buf [8]byte
	n := to - from
	if n <= 0 || n > 8 || !readFull(r, buf[:n], from) {
		return 0
	}
	var value uint64
	for _, c := range buf[:n] {
		value = value<<8 | uint64(c)
	}
	return value
}

func matroskaSize(r io.ReaderAt, end int64) (width, height int) {
	ebmlChildren(r, 0, end, func(id uint32, from, to int64) bool {
		if id != segmentID {
			return true
		}
		ebmlChildren(r, from, to, func(id uint32, from, to int64) bool {
			switch id {
			case clusterID:
				return false
			case tracksID:
				ebmlChildren(r, from, to, func(id uint32, from, to int64) bool {
					if id != trackEntryID {
						return true
					}
					w, h, video := matroskaTrack(r, from, to)
					if video && w > 0 && h > 0 {
						width, height = w, h
						return false
					}
					return true
				})
				return false
			}
			return true
		})
		return false
	})
	return width, height
}

// matroskaTrack is a track's shown size. The display size is pixels when its
// unit says so (the default), and only a shape in any other unit, which the
// pixel height then scales.
func matroskaTrack(r io.ReaderAt, from, to int64) (width, height int, video bool) {
	var pixelW, pixelH, displayW, displayH, unit uint64
	ebmlChildren(r, from, to, func(id uint32, from, to int64) bool {
		switch id {
		case trackTypeID:
			video = ebmlUint(r, from, to) == videoTrackType
		case videoID:
			ebmlChildren(r, from, to, func(id uint32, from, to int64) bool {
				switch id {
				case pixelWidthID:
					pixelW = ebmlUint(r, from, to)
				case pixelHeightID:
					pixelH = ebmlUint(r, from, to)
				case displayWidthID:
					displayW = ebmlUint(r, from, to)
				case displayHeightID:
					displayH = ebmlUint(r, from, to)
				case displayUnitID:
					unit = ebmlUint(r, from, to)
				}
				return true
			})
		}
		return true
	})
	if pixelW == 0 || pixelH == 0 || pixelW > maxSide || pixelH > maxSide {
		return 0, 0, video
	}
	if displayW == 0 || displayH == 0 || displayW > maxSide || displayH > maxSide {
		return int(pixelW), int(pixelH), video
	}
	if unit == 0 {
		return int(displayW), int(displayH), video
	}
	return int(pixelH * displayW / displayH), int(pixelH), video
}

func readFull(r io.ReaderAt, buf []byte, at int64) bool {
	n, err := r.ReadAt(buf, at)
	return n == len(buf) && (err == nil || err == io.EOF)
}
