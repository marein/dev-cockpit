package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/marein/dev-cockpit/internal/assistant"
)

// codex writes how full its context stands into the session's rollout, one
// token_count event per request (verified on 0.172.0): what the last request
// took in, cached or not, and the window of the model that answered. The
// model's name stands on the turn_context record at the start of each turn.
// The last of each is where the conversation stands.

type contextLine struct {
	Type    string `json:"type"`
	Payload struct {
		Type  string `json:"type"`
		Model string `json:"model"`
		Info  *struct {
			Last struct {
				InputTokens int `json:"input_tokens"`
			} `json:"last_token_usage"`
			Window int `json:"model_context_window"`
		} `json:"info"`
	} `json:"payload"`
}

// contextUsage reports how full the context of this session stood after its
// last request. The second return is false when the rollout holds no such
// record: a turn that failed before its first answer never wrote one.
func (r *sessionRepository) contextUsage(sessionID string) (assistant.ContextUsage, bool) {
	path, err := r.rollout(sessionID)
	if err != nil {
		return assistant.ContextUsage{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return assistant.ContextUsage{}, false
	}
	defer func() { _ = f.Close() }()
	return readContextUsage(f)
}

func readContextUsage(source io.Reader) (assistant.ContextUsage, bool) {
	reader := bufio.NewReaderSize(source, 64<<10)
	usage := assistant.ContextUsage{}
	for {
		raw, err := readLine(reader, recordLineLimit)
		// A compaction leaves no reading of its own: the token count codex
		// writes behind it takes in nothing, so the context stands unknown
		// until the next request.
		if compactedRecord(raw) {
			usage.Tokens = 0
		}
		// Two records matter and each names a field little else carries, so
		// the cheap check comes before the parse.
		if bytes.Contains(raw, []byte(`"token_count"`)) || bytes.Contains(raw, []byte(`"turn_context"`)) {
			var line contextLine
			if json.Unmarshal(raw, &line) == nil {
				switch {
				case line.Type == "turn_context" && line.Payload.Model != "":
					usage.Model = line.Payload.Model
				case line.Type == recordEvent && line.Payload.Type == "token_count" && line.Payload.Info != nil && line.Payload.Info.Last.InputTokens > 0:
					usage.Tokens = line.Payload.Info.Last.InputTokens
					usage.Window = line.Payload.Info.Window
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return assistant.ContextUsage{}, false
		}
	}
	if usage.Window <= 0 {
		usage.Window = assistant.ContextWindow("codex", usage.Model, "")
	}
	return usage, usage.Tokens > 0
}
