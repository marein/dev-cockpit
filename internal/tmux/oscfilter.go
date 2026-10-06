package tmux

// maxOSCPayload caps how much of one OSC sequence FilterMarks keeps. The
// interesting payloads (prompt marks like "133;C") are tiny; anything bigger
// is dropped, not truncated, so callers never match on a partial payload.
const maxOSCPayload = 128

const (
	oscGround = iota
	oscEscape
	oscString
)

// OSCFilter splits a pane stream for the watchers into the bytes outside OSC
// sequences and the payloads of the OSC sequences, carrying its state across
// chunks. It ends an OSC where xterm.js does, on BEL, any ESC, CAN, SUB or the
// C1 ST, so the BEL that ends one is never taken for a bell and a sequence
// that is never terminated hides nothing past the next escape.
type OSCFilter struct {
	state    int
	c2       bool
	payload  []byte
	overflow bool
}

// Reset clears carried state; call it whenever the stream restarts.
func (f *OSCFilter) Reset() {
	*f = OSCFilter{}
}

// FilterMarks returns chunk without its OSC sequences and the payloads of
// every OSC sequence completed within it.
func (f *OSCFilter) FilterMarks(chunk []byte) ([]byte, []string) {
	var out []byte
	var marks []string
	for _, b := range chunk {
		switch f.state {
		case oscString:
			c2 := f.c2
			f.c2 = b == 0xc2
			switch {
			case b == 0x07 || b == 0x1b || c2 && b == 0x9c:
				if c2 && b == 0x9c && !f.overflow {
					f.payload = f.payload[:len(f.payload)-1]
				}
				if !f.overflow {
					marks = append(marks, string(f.payload))
				}
				f.state = oscGround
				if b == 0x1b {
					f.state = oscEscape
				}
			case b == 0x18 || b == 0x1a:
				f.state = oscGround
			case f.overflow:
			case len(f.payload) >= maxOSCPayload:
				f.overflow = true
				f.payload = nil
			default:
				f.payload = append(f.payload, b)
			}
		case oscEscape:
			switch b {
			case ']':
				f.state = oscString
				f.payload = nil
				f.overflow = false
			case 0x1b:
				out = append(out, b)
			case '\\':
				f.state = oscGround
			default:
				f.state = oscGround
				out = append(out, 0x1b, b)
			}
		default:
			if f.c2 {
				f.c2 = false
				if b == 0x9d {
					f.state = oscString
					f.payload = nil
					f.overflow = false
					continue
				}
				out = append(out, 0xc2)
			}
			switch b {
			case 0x1b:
				f.state = oscEscape
			case 0xc2:
				f.c2 = true
			default:
				out = append(out, b)
			}
		}
	}
	return out, marks
}
