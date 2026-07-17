package editorintelligence

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// maxFrameBytes caps a single LSP frame in both directions. Completion
// payloads stay far below this; anything larger indicates a broken or
// hostile peer.
const maxFrameBytes = 16 << 20

// writeFrame writes one Content-Length framed JSON-RPC payload. The caller
// serializes writes.
func writeFrame(w io.Writer, payload []byte) error {
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// readFrame reads one Content-Length framed JSON-RPC payload.
func readFrame(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, found := strings.Cut(line, ":")
		if !found {
			return nil, fmt.Errorf("malformed header line %q", line)
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil {
				return nil, fmt.Errorf("malformed Content-Length %q", value)
			}
			length = n
		}
	}
	if length < 0 {
		return nil, errors.New("frame without Content-Length")
	}
	if length > maxFrameBytes {
		return nil, fmt.Errorf("frame of %d bytes exceeds the limit", length)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}
	return payload, nil
}

// rpcMessage is the wire shape shared by requests, responses and
// notifications.
type rpcMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("language server error %d: %s", e.Code, e.Message)
}

func rawID(id int64) *json.RawMessage {
	raw := json.RawMessage(strconv.FormatInt(id, 10))
	return &raw
}
