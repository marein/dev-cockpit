package term

import "testing"

func TestModeTracker(t *testing.T) {
	var m modeTracker

	m.feed([]byte("\x1b[?1h"))
	if app, _ := m.snapshot(); !app {
		t.Error("DECCKM should be set after ?1h")
	}
	m.feed([]byte("\x1b[?1l"))
	if app, _ := m.snapshot(); app {
		t.Error("DECCKM should be reset after ?1l")
	}

	m.feed([]byte("\x1b[?2004h"))
	if _, br := m.snapshot(); !br {
		t.Error("bracketed paste should be set after ?2004h")
	}

	// Other private modes must not flip our flags.
	m.feed([]byte("\x1b[?1049h"))
	if _, br := m.snapshot(); !br {
		t.Error("?1049h must not clear bracketed paste")
	}
	if app, _ := m.snapshot(); app {
		t.Error("?1049h must not set DECCKM")
	}
}

func TestModeTrackerSplitChunks(t *testing.T) {
	var m modeTracker
	m.feed([]byte("\x1b[?"))
	m.feed([]byte("1"))
	m.feed([]byte("h"))
	if app, _ := m.snapshot(); !app {
		t.Error("DECCKM should be set across split chunks")
	}
}
