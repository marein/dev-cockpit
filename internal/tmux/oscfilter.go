package tmux

// maxOSCPayload caps how much of one OSC sequence FilterMarks keeps. The
// interesting payloads (prompt marks like "133;C") are tiny; anything bigger
// is dropped, not truncated, so callers never match on a partial payload.
const maxOSCPayload = 128

// OSCFilter strips OSC escape sequences (e.g. terminal title updates) from a
// byte stream. Unlike a stateless strip it carries its state across chunks,
// so sequences split at arbitrary read boundaries cannot leak through.
type OSCFilter struct {
	inOSC     bool
	pending   byte
	payload   []byte
	overflow  bool
	head      []byte
	hyperlink bool
}

// hyperlinkPrefix starts an OSC 8 hyperlink. Filter keeps these for the
// browser terminal and ends them with ST, so a BEL terminator never reaches
// a consumer as a bell.
const hyperlinkPrefix = "8;"

// Reset clears carried state; call it whenever the stream restarts.
func (f *OSCFilter) Reset() {
	f.inOSC = false
	f.pending = 0
	f.payload = nil
	f.overflow = false
	f.head = nil
	f.hyperlink = false
}

// Filter returns chunk with OSC sequences other than hyperlinks removed, holding back an
// unterminated sequence (or a trailing ESC or 0xc2) until the next chunk.
// An OSC may also start with the C1 code U+009D, which xterm.js honors too.
func (f *OSCFilter) Filter(chunk []byte) []byte {
	out, _ := f.filter(chunk, false)
	return out
}

// FilterMarks behaves like Filter and additionally returns the payloads of
// every OSC sequence completed within this chunk (state carries across
// chunks, so split sequences still yield one complete payload).
func (f *OSCFilter) FilterMarks(chunk []byte) ([]byte, []string) {
	return f.filter(chunk, true)
}

func (f *OSCFilter) filter(chunk []byte, collect bool) ([]byte, []string) {
	if len(chunk) == 0 {
		return nil, nil
	}
	src := chunk
	if f.pending != 0 {
		src = append([]byte{f.pending}, chunk...)
		f.pending = 0
	}
	dst := make([]byte, 0, len(src))
	var marks []string
	finish := func() {
		if f.hyperlink {
			dst = append(dst, 0x1b, '\\')
		}
		f.inOSC = false
		f.head = nil
		f.hyperlink = false
		if collect && !f.overflow {
			marks = append(marks, string(f.payload))
		}
		f.payload = nil
		f.overflow = false
	}
	remember := func(b byte) {
		if !collect || f.overflow {
			return
		}
		if len(f.payload) >= maxOSCPayload {
			f.overflow = true
			f.payload = nil
			return
		}
		f.payload = append(f.payload, b)
	}
	i := 0
	for i < len(src) {
		if f.inOSC {
			switch {
			case src[i] == 0x07:
				finish()
				i++
			case src[i] == 0x1b && i+1 == len(src):
				// Possibly the first half of an ESC \ terminator.
				f.pending = 0x1b
				i++
			case src[i] == 0x1b && src[i+1] == '\\':
				finish()
				i += 2
			case f.hyperlink && (src[i] < 0x20 || src[i] > 0x7e):
				// A link holds printable ASCII only. Any other byte could end
				// it early in xterm.js and start a sequence of its own, so the
				// link is aborted with CAN and the rest is stripped as on any
				// other OSC.
				dst = append(dst, 0x18)
				f.hyperlink = false
				i++
			default:
				remember(src[i])
				switch {
				case f.hyperlink:
					dst = append(dst, src[i])
				case !collect && len(f.head) < len(hyperlinkPrefix):
					f.head = append(f.head, src[i])
					if string(f.head) == hyperlinkPrefix {
						f.hyperlink = true
						dst = append(dst, 0x1b, ']')
						dst = append(dst, f.head...)
					}
				}
				i++
			}
			continue
		}
		if src[i] != 0x1b && src[i] != 0xc2 {
			dst = append(dst, src[i])
			i++
			continue
		}
		if i+1 == len(src) {
			f.pending = src[i]
			i++
			continue
		}
		if src[i] == 0x1b && src[i+1] == ']' || src[i] == 0xc2 && src[i+1] == 0x9d {
			f.inOSC = true
			f.payload = nil
			f.overflow = false
			i += 2
			continue
		}
		dst = append(dst, src[i])
		i++
	}
	return dst, marks
}

// stripOSC removes OSC sequences from a complete buffer (e.g. a pane capture).
func stripOSC(src []byte) []byte {
	var f OSCFilter
	return f.Filter(src)
}
