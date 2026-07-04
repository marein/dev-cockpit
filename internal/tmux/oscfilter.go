package tmux

// maxOSCPayload caps how much of one OSC sequence FilterMarks keeps. The
// interesting payloads (prompt marks like "133;C") are tiny; anything bigger
// is dropped, not truncated, so callers never match on a partial payload.
const maxOSCPayload = 128

// OSCFilter strips OSC escape sequences (e.g. terminal title updates) from a
// byte stream. Unlike a stateless strip it carries its state across chunks,
// so sequences split at arbitrary read boundaries cannot leak through.
type OSCFilter struct {
	inOSC      bool
	pendingEsc bool
	payload    []byte
	overflow   bool
}

// Reset clears carried state; call it whenever the stream restarts.
func (f *OSCFilter) Reset() {
	f.inOSC = false
	f.pendingEsc = false
	f.payload = nil
	f.overflow = false
}

// Filter returns chunk with OSC sequences removed, holding back an
// unterminated sequence (or trailing lone ESC) until the next chunk.
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
	if f.pendingEsc {
		f.pendingEsc = false
		src = append([]byte{0x1b}, chunk...)
	}
	dst := make([]byte, 0, len(src))
	var marks []string
	finish := func() {
		f.inOSC = false
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
			case src[i] == 0x1b && i+1 < len(src) && src[i+1] == '\\':
				finish()
				i += 2
			case src[i] == 0x1b && i+1 == len(src):
				// Possibly the first half of an ESC \ terminator.
				f.pendingEsc = true
				i++
			default:
				remember(src[i])
				i++
			}
			continue
		}
		if src[i] != 0x1b {
			dst = append(dst, src[i])
			i++
			continue
		}
		if i+1 == len(src) {
			f.pendingEsc = true
			i++
			continue
		}
		if src[i+1] == ']' {
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
