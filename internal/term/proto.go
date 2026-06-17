package term

import (
	"encoding/binary"
	"errors"
	"io"
)

// Wire protocol between the serve process (and the attach CLI) and a session
// agent over a unix socket. Every message is a frame:
//
//	[type:1][len:4 big-endian][payload:len]
//
// The same connection carries both directions: the agent emits frameOutput as
// the PTY produces bytes; a client sends the input/control frames below.
const (
	frameOutput = 'o' // agent -> client: raw PTY bytes

	frameInput  = 'i' // client -> agent: raw bytes written verbatim to the PTY
	frameKey    = 'k' // client -> agent: a named key (mode-aware translation)
	framePaste  = 'p' // client -> agent: text pasted (bracketed when the app asked)
	frameResize = 'r' // client -> agent: payload = cols:2,rows:2 big-endian
	frameRedraw = 'd' // client -> agent: nudge the program into a full repaint
)

// maxFramePayload bounds a single frame so a malformed length cannot make the
// reader allocate without limit.
const maxFramePayload = 1 << 20 // 1 MiB

var errFrameTooLarge = errors.New("terminal frame exceeds maximum size")

func writeFrame(w io.Writer, typ byte, payload []byte) error {
	var header [5]byte
	header[0] = typ
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := w.Write(payload)
	return err
}

func readFrame(r io.Reader) (typ byte, payload []byte, err error) {
	var header [5]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(header[1:])
	if n > maxFramePayload {
		return 0, nil, errFrameTooLarge
	}
	if n == 0 {
		return header[0], nil, nil
	}
	payload = make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return header[0], payload, nil
}

func resizePayload(cols, rows int) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint16(b[0:], clampUint16(cols))
	binary.BigEndian.PutUint16(b[2:], clampUint16(rows))
	return b
}

func parseResize(payload []byte) (cols, rows int, ok bool) {
	if len(payload) != 4 {
		return 0, 0, false
	}
	return int(binary.BigEndian.Uint16(payload[0:])), int(binary.BigEndian.Uint16(payload[2:])), true
}

func clampUint16(v int) uint16 {
	if v < 0 {
		return 0
	}
	if v > 0xffff {
		return 0xffff
	}
	return uint16(v)
}
