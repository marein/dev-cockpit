package copilot

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"

	"github.com/marein/dev-cockpit/internal/assistant"
)

// copilot keeps how full its context stands out of its output entirely. It
// records it in the session's own event log instead, on the record it writes
// when a run shuts down, together with the model that answered. A conversation
// turn is one non-interactive run, so that record lands once per turn and the
// log carries one per turn taken so far; the last one is where the conversation
// stands.

// coderID is this coder's own name, which is what the window table is keyed by:
// the same model does not have the same window under every CLI.
const coderID = "copilot"

// contextTailBytes is how much of the end of an event log is read to find that
// record. It is the last line of a finished run, so the tail is generous enough
// for a single long record and nothing else: the log itself grows into
// megabytes of tool payloads.
const contextTailBytes = 64 << 10

// contextRecord is the part of the two records a context reading needs.
// session.shutdown carries what the context held (currentTokens is what
// copilot's own statusline counts, the system and tool definition parts are
// already inside it), and session.model_change carries the context tier, which
// is what decides the window: a model reachable under a wide tier has a far
// larger bound there than under the standard one.
type contextRecord struct {
	Type string `json:"type"`
	Data struct {
		CurrentModel  string `json:"currentModel"`
		CurrentTokens int    `json:"currentTokens"`
		NewModel      string `json:"newModel"`
		ContextTier   string `json:"contextTier"`
	} `json:"data"`
}

// contextUsage reports how full the context of this session stood when its last
// run ended. The second return is false when the log holds no such record: a
// turn that was killed never wrote one, and an older CLI may not write one at
// all.
func (r *sessionRepository) contextUsage(sessionID string) (assistant.ContextUsage, bool) {
	path, err := r.eventsFile(sessionID)
	if err != nil {
		return assistant.ContextUsage{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return assistant.ContextUsage{}, false
	}
	defer f.Close()

	// Only the end of the log is read, and the first line after a seek is
	// dropped: it is a fragment.
	var reader *bufio.Reader
	if info, err := f.Stat(); err == nil && info.Size() > contextTailBytes {
		if _, err := f.Seek(info.Size()-contextTailBytes, io.SeekStart); err != nil {
			return assistant.ContextUsage{}, false
		}
		reader = bufio.NewReader(f)
		if _, err := reader.ReadString('\n'); err != nil {
			return assistant.ContextUsage{}, false
		}
	} else {
		reader = bufio.NewReader(f)
	}
	return readContextUsage(reader)
}

// readContextUsage picks the last readable shutdown record out of an event log,
// measured against the tier the session was last put on. The last of each wins:
// a resumed session carries one shutdown record per run, and only the newest
// says where the conversation stands now.
func readContextUsage(source io.Reader) (assistant.ContextUsage, bool) {
	scanner := bufio.NewScanner(source)
	scanner.Buffer(make([]byte, 64*1024), contextTailBytes+4096)
	usage := assistant.ContextUsage{}
	found := false
	tier := ""
	for scanner.Scan() {
		line := scanner.Bytes()
		// Only two records matter and both name a field nothing else carries, so
		// the cheap check comes before the parse. The shutdown record is one of
		// the largest in the log.
		hasTokens := bytes.Contains(line, []byte(`"currentTokens"`))
		if !hasTokens && !bytes.Contains(line, []byte(`"contextTier"`)) {
			continue
		}
		var rec contextRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec.Type == "session.model_change" {
			// A change that names no tier says the session is on the standard
			// one, so an earlier wide tier does not carry over.
			tier = rec.Data.ContextTier
			continue
		}
		if !hasTokens || rec.Data.CurrentTokens <= 0 {
			continue
		}
		usage = assistant.ContextUsage{
			Model:  rec.Data.CurrentModel,
			Tier:   tier,
			Tokens: rec.Data.CurrentTokens,
			Window: assistant.ContextWindow(coderID, rec.Data.CurrentModel, tier),
		}
		found = true
	}
	// A record already read stands even when the rest of the tail does not: the
	// log is written by another process and the end of it may be half a line.
	if err := scanner.Err(); err != nil && !found {
		return assistant.ContextUsage{}, false
	}
	return usage, found
}
