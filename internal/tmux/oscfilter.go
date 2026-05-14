package tmux

// OSCFilter strips OSC escape sequences (e.g. terminal title updates) from a
// byte stream. Unlike a stateless strip it carries its state across chunks,
// so sequences split at arbitrary read boundaries cannot leak through.
type OSCFilter struct {
	inOSC      bool
	pendingEsc bool
}

// Reset clears carried state; call it whenever the stream restarts.
func (f *OSCFilter) Reset() {
	f.inOSC = false
	f.pendingEsc = false
}

// Filter returns chunk with OSC sequences removed, holding back an
// unterminated sequence (or trailing lone ESC) until the next chunk.
func (f *OSCFilter) Filter(chunk []byte) []byte {
	if len(chunk) == 0 {
		return nil
	}
	src := chunk
	if f.pendingEsc {
		f.pendingEsc = false
		src = append([]byte{0x1b}, chunk...)
	}
	dst := make([]byte, 0, len(src))
	i := 0
	for i < len(src) {
		if f.inOSC {
			switch {
			case src[i] == 0x07:
				f.inOSC = false
				i++
			case src[i] == 0x1b && i+1 < len(src) && src[i+1] == '\\':
				f.inOSC = false
				i += 2
			case src[i] == 0x1b && i+1 == len(src):
				// Possibly the first half of an ESC \ terminator.
				f.pendingEsc = true
				i++
			default:
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
			i += 2
			continue
		}
		dst = append(dst, src[i])
		i++
	}
	return dst
}

// stripOSC removes OSC sequences from a complete buffer (e.g. a pane capture).
func stripOSC(src []byte) []byte {
	var f OSCFilter
	return f.Filter(src)
}
