package statusline

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
)

// tokenSums is what every turn of a conversation spent, read out of its
// transcript. counted is false while no turn carried a usage record, which is
// every session before its first answer: four zeroes nobody measured are no
// answer.
type tokenSums struct {
	input, output, read, write float64
	counted                    bool
}

// readSums adds up the transcript in one pass. It is the only thing the
// payload cannot answer: its counts are the last request's alone.
//
// A turn is a request, not a line. claude writes one record per content block
// of an answer, thinking, text and every tool call, and every one of them
// carries the **same** usage object of the whole request, so adding the records
// up counts a turn as often as it had blocks: measured against the real
// transcripts on this machine that is roughly twice the tokens actually spent.
// The request id is what tells one turn from the next, and a record that has
// none at all is counted as it stands rather than folded into a shared empty
// key. A file whose last line is half written, which is every file claude is
// appending to right now, ends the read instead of losing the sums.
func readSums(path string) tokenSums {
	var sums tokenSums
	if path == "" {
		return sums
	}
	file, err := os.Open(path)
	if err != nil {
		return sums
	}
	defer file.Close()
	seen := map[string]bool{}
	decoder := json.NewDecoder(bufio.NewReader(file))
	for {
		var record map[string]json.RawMessage
		if err := decoder.Decode(&record); err != nil {
			// A line that is valid JSON but no object is skipped, it was read
			// to its end; anything else is a record that is not complete yet.
			var typeErr *json.UnmarshalTypeError
			if errors.As(err, &typeErr) {
				continue
			}
			break
		}
		usage, ok := recordUsage(record)
		if !ok {
			continue
		}
		id := recordID(record)
		if id != "" {
			if seen[id] {
				continue
			}
			seen[id] = true
		}
		sums.counted = true
		sums.input += orZero(usage["input_tokens"])
		sums.output += orZero(usage["output_tokens"])
		sums.read += orZero(usage["cache_read_input_tokens"])
		sums.write += orZero(usage["cache_creation_input_tokens"])
	}
	return sums
}

func recordUsage(record map[string]json.RawMessage) (map[string]any, bool) {
	var message struct {
		Usage map[string]any `json:"usage"`
	}
	if json.Unmarshal(record["message"], &message) != nil || message.Usage == nil {
		return nil, false
	}
	return message.Usage, true
}

// recordID is the request a record belongs to, its own id where it names no
// request.
func recordID(record map[string]json.RawMessage) string {
	for _, key := range []string{"requestId", "uuid"} {
		var id string
		if json.Unmarshal(record[key], &id) == nil && id != "" {
			return id
		}
	}
	return ""
}
