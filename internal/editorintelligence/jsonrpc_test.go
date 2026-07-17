package editorintelligence

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

// chunkReader hands out one byte per read, proving the frame reader survives
// partial reads.
type chunkReader struct{ r io.Reader }

func (c chunkReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return c.r.Read(p)
}

func TestFrameRoundtrip(t *testing.T) {
	var buf bytes.Buffer
	payload := []byte(`{"jsonrpc":"2.0","id":1,"method":"x"}`)
	if err := writeFrame(&buf, payload); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}
	got, err := readFrame(bufio.NewReader(chunkReader{&buf}))
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: %q", got)
	}
}

func TestFrameExtraHeadersIgnored(t *testing.T) {
	raw := "Content-Type: application/vscode-jsonrpc; charset=utf-8\r\ncontent-length: 2\r\n\r\n{}"
	got, err := readFrame(bufio.NewReader(strings.NewReader(raw)))
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if string(got) != "{}" {
		t.Fatalf("got %q", got)
	}
}

func TestFrameMalformedHeader(t *testing.T) {
	cases := []string{
		"NoColonHere\r\n\r\n{}",
		"Content-Length: abc\r\n\r\n{}",
		"Content-Type: x\r\n\r\n{}",
	}
	for _, raw := range cases {
		if _, err := readFrame(bufio.NewReader(strings.NewReader(raw))); err == nil {
			t.Fatalf("expected error for %q", raw)
		}
	}
}

func TestFrameTooLarge(t *testing.T) {
	raw := fmt.Sprintf("Content-Length: %d\r\n\r\n", maxFrameBytes+1)
	if _, err := readFrame(bufio.NewReader(strings.NewReader(raw))); err == nil {
		t.Fatal("expected size error")
	}
}

func TestFrameTruncatedBody(t *testing.T) {
	raw := "Content-Length: 10\r\n\r\n{}"
	if _, err := readFrame(bufio.NewReader(strings.NewReader(raw))); err == nil {
		t.Fatal("expected read error")
	}
}

func TestDecodeCompletionResultShapes(t *testing.T) {
	items, incomplete, err := decodeCompletionResult(json.RawMessage(`null`))
	if err != nil || items != nil || incomplete {
		t.Fatalf("null result: %v %v %v", items, incomplete, err)
	}
	items, _, err = decodeCompletionResult(json.RawMessage(`[{"label":"a"}]`))
	if err != nil || len(items) != 1 || items[0].Label != "a" {
		t.Fatalf("array result: %v %v", items, err)
	}
	items, incomplete, err = decodeCompletionResult(json.RawMessage(`{"isIncomplete":true,"items":[{"label":"b"}]}`))
	if err != nil || !incomplete || len(items) != 1 || items[0].Label != "b" {
		t.Fatalf("list result: %v %v %v", items, incomplete, err)
	}
}
