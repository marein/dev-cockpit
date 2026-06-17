package term

import "sync"

// modeTracker follows the two DEC private modes the input path needs: DECCKM
// (?1, application cursor keys) and bracketed paste (?2004). It scans the raw
// PTY output non-destructively and carries parser state across read chunks, so
// a sequence split at a boundary is still recognised.
//
// It only inspects CSI ? ... [hl] sequences and ignores everything else, so it
// is a tiny state machine rather than a full terminal parser.
type modeTracker struct {
	mu          sync.Mutex
	appCursor   bool
	bracketed   bool
	state       scanState
	params      []int
	current     int
	haveCurrent bool
}

type scanState int

const (
	scanGround  scanState = iota
	scanEsc               // saw ESC
	scanCSI               // saw ESC [
	scanPrivate           // saw ESC [ ?  -> collecting numeric params
)

func (m *modeTracker) feed(b []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range b {
		switch m.state {
		case scanGround:
			if c == 0x1b {
				m.state = scanEsc
			}
		case scanEsc:
			if c == '[' {
				m.state = scanCSI
			} else {
				m.state = scanGround
			}
		case scanCSI:
			if c == '?' {
				m.state = scanPrivate
				m.params = m.params[:0]
				m.current = 0
				m.haveCurrent = false
			} else {
				// Some other CSI sequence; we don't care about it.
				m.state = scanGround
			}
		case scanPrivate:
			switch {
			case c >= '0' && c <= '9':
				m.current = m.current*10 + int(c-'0')
				m.haveCurrent = true
			case c == ';':
				m.pushParam()
			case c == 'h' || c == 'l':
				m.pushParam()
				m.apply(c == 'h')
				m.state = scanGround
			default:
				m.state = scanGround
			}
		}
	}
}

func (m *modeTracker) pushParam() {
	if m.haveCurrent {
		m.params = append(m.params, m.current)
	}
	m.current = 0
	m.haveCurrent = false
}

func (m *modeTracker) apply(set bool) {
	for _, p := range m.params {
		switch p {
		case 1:
			m.appCursor = set
		case 2004:
			m.bracketed = set
		}
	}
}

func (m *modeTracker) snapshot() (appCursor, bracketed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.appCursor, m.bracketed
}
