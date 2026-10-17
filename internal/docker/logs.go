package docker

import (
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Logs reads the last lines a container wrote, both of its streams in the
// order they came, as plain text: the terminal's escape sequences a program
// colours its output with are taken out, a page shows them as text.
func (c *Client) Logs(ctx context.Context, id string, tail int) (string, error) {
	query := url.Values{"stdout": {"1"}, "stderr": {"1"}, "tail": {strconv.Itoa(tail)}}
	res, err := c.do(ctx, http.MethodGet, "/containers/"+url.PathEscape(id)+"/logs", query)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxBodySize))
	if err != nil {
		return "", err
	}
	if framed, ok := unframeLogs(raw); ok {
		raw = framed
	}
	return strings.ToValidUTF8(logEscapes.ReplaceAllString(string(raw), ""), "�"), nil
}

// unframeLogs unwraps the log of a container without a tty: the daemon
// writes it as frames, each behind a header of eight bytes naming the stream
// and the length. A log that does not read as frames throughout is a tty's
// and stays as it is. A last frame the size cap cut short counts.
func unframeLogs(raw []byte) ([]byte, bool) {
	var out []byte
	for len(raw) > 0 {
		if len(raw) < 8 || raw[0] > 2 || raw[1] != 0 || raw[2] != 0 || raw[3] != 0 {
			return nil, false
		}
		size := int(binary.BigEndian.Uint32(raw[4:8]))
		raw = raw[8:]
		if size > len(raw) {
			size = len(raw)
		}
		out = append(out, raw[:size]...)
		raw = raw[size:]
	}
	return out, true
}

// logEscapes are the terminal sequences a log line carries: the CSI ones
// colours and cursor moves are made of, and the OSC ones a title or a link is.
var logEscapes = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[@-Z\\-_]`)
