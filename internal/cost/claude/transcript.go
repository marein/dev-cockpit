package claude

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/marein/dev-cockpit/internal/cost"
	"github.com/marein/dev-cockpit/internal/cost/price"
)

// headSize is how much of a file's start identifies it: a file replaced
// under the same name with another start is read again from the top.
const headSize = 256

type fileCursor struct {
	Size   int64  `json:"size"`
	Offset int64  `json:"offset"`
	Head   string `json:"head,omitempty"`
	CWD    string `json:"cwd,omitempty"`
	// At is the newest timestamp read so far, the time a cost-state line
	// that carries none of its own is booked at.
	At time.Time `json:"at,omitzero"`
	// Last is the call of the last line read. claude writes one line per
	// content block of a response, each with the response's usage, and a
	// read may end between them.
	Last *lastCall `json:"last,omitempty"`
}

type lastCall struct {
	Key    string       `json:"key"`
	Tokens price.Tokens `json:"tokens"`
	// Copied says another session booked the call first.
	Copied bool `json:"copied,omitempty"`
}

type reader struct {
	src     *Source
	state   *state
	owner   map[string]string
	table   price.Table
	now     time.Time
	entries []cost.Entry
}

func (r *reader) session(id string) *sessionState {
	ss := r.state.Sessions[id]
	if ss == nil {
		ss = &sessionState{}
		r.state.Sessions[id] = ss
	}
	return ss
}

// readDisk reads a transcript on disk.
func (r *reader) readDisk(t transcript, c fileCursor) (fileCursor, error) {
	f, err := os.Open(t.path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return c, err
	}
	t.modTime = info.ModTime()
	return r.readFile(t, c, f, info.Size())
}

// readFile reads what a transcript of size gained since the cursor, out of
// a file on disk or out of a capture.
func (r *reader) readFile(t transcript, c fileCursor, f io.ReaderAt, size int64) (fileCursor, error) {
	if size == c.Size && c.Size > 0 {
		return c, nil
	}
	head, err := readHead(f)
	if err != nil {
		return c, err
	}
	if size < c.Offset || c.Head != "" && head != "" && head != c.Head {
		c = fileCursor{}
	}
	if head != "" {
		c.Head = head
	}
	in := bufio.NewReaderSize(io.NewSectionReader(f, c.Offset, size-c.Offset), 64*1024)
	offset := c.Offset
	for {
		line, err := in.ReadBytes('\n')
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return c, err
			}
			break
		}
		offset += int64(len(line))
		if at, ok := lineTime(line); ok && at.After(c.At) {
			c.At = at
		}
		if c.CWD == "" && !t.sub {
			c.CWD = lineCWD(line)
		}
		if bytes.Contains(line, usageMarker) {
			r.call(t, &c, line)
		} else if bytes.Contains(line, costStateMarker) {
			r.costState(t, &c, line)
		}
	}
	c.Offset = offset
	c.Size = size
	return c, nil
}

// cwdOf is where a file's calls ran when a line does not say: the file's
// first cwd, else the directory name read against the known places.
func (r *reader) cwdOf(t transcript, c *fileCursor) string {
	if c.CWD != "" {
		return c.CWD
	}
	return r.src.placeOf(t.dir)
}

var (
	usageMarker     = []byte(`"usage":{`)
	costStateMarker = []byte(`"cost-state"`)
	cwdMarker       = []byte(`"cwd":"`)
	timeMarker      = []byte(`"timestamp":"`)
)

type assistantLine struct {
	Type       string  `json:"type"`
	Timestamp  string  `json:"timestamp"`
	CWD        string  `json:"cwd"`
	RequestID  *string `json:"requestId"`
	IsAPIError bool    `json:"isApiErrorMessage"`
	Message    *struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *usage `json:"usage"`
	} `json:"message"`
}

type usage struct {
	Input         int64 `json:"input_tokens"`
	Output        int64 `json:"output_tokens"`
	CacheRead     int64 `json:"cache_read_input_tokens"`
	CacheCreation int64 `json:"cache_creation_input_tokens"`
	CacheSplit    *struct {
		FiveMinutes int64 `json:"ephemeral_5m_input_tokens"`
		OneHour     int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
	ServerTools *struct {
		WebSearch int64 `json:"web_search_requests"`
	} `json:"server_tool_use"`
	Speed string `json:"speed"`
}

// tokens reads a usage into price classes. Output holds the thinking
// tokens already. A usage without the cache write split predates the one
// hour cache, its writes are the five minute default.
func (u usage) tokens() price.Tokens {
	t := price.Tokens{Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite5m: u.CacheCreation}
	if u.CacheSplit != nil {
		t.CacheWrite5m = u.CacheSplit.FiveMinutes
		t.CacheWrite1h = u.CacheSplit.OneHour
		if rest := u.CacheCreation - t.CacheWrite5m - t.CacheWrite1h; rest > 0 {
			t.CacheWrite5m += rest
		}
	}
	if u.ServerTools != nil {
		t.WebSearch = u.ServerTools.WebSearch
	}
	return t
}

// call books one assistant line. Lines without a call id, claude's own
// <synthetic> notices and API error lines carry no spend.
func (r *reader) call(t transcript, c *fileCursor, line []byte) {
	var l assistantLine
	if json.Unmarshal(line, &l) != nil || l.Type != "assistant" || l.IsAPIError {
		return
	}
	m := l.Message
	if m == nil || m.Usage == nil || m.ID == "" || m.Model == "" || m.Model == "<synthetic>" {
		return
	}
	tokens := m.Usage.tokens()
	if tokens == (price.Tokens{}) {
		return
	}
	id := m.ID
	if l.RequestID != nil {
		id += "/" + *l.RequestID
	}
	key := callKey(id)
	ss := r.session(t.session)
	if last := c.Last; last != nil && last.Key == key {
		grown := growth(tokens, last.Tokens)
		if grown == (price.Tokens{}) {
			return
		}
		last.Tokens.Add(grown)
		r.spend(t, c, ss, l, grown, last.Copied)
		return
	}
	owner, seen := r.owner[key]
	c.Last = &lastCall{Key: key, Tokens: tokens, Copied: seen && owner != t.session}
	if seen && owner == t.session {
		return
	}
	if !seen {
		r.owner[key] = t.session
		ss.Messages = append(ss.Messages, key)
	}
	r.spend(t, c, ss, l, tokens, seen)
}

// spend prices tokens of one call. A copied call only counts towards the
// session's copied sum, the session that booked it first holds the spend.
func (r *reader) spend(t transcript, c *fileCursor, ss *sessionState, l assistantLine, tokens price.Tokens, copied bool) {
	model := price.BaseModel(l.Message.Model)
	rate, priced := r.table.Lookup(price.Anthropic, model)
	usd := 0.0
	if priced {
		usd = rate.Cost(tokens, l.Message.Usage.Speed == "fast")
	}
	if copied {
		addUSD(&ss.Copied, model, usd)
		return
	}
	addUSD(&ss.Booked, model, usd)
	at, err := time.Parse(time.RFC3339Nano, l.Timestamp)
	if err != nil {
		at = c.At
	}
	cwd := l.CWD
	if cwd == "" {
		cwd = r.cwdOf(t, c)
	}
	r.entries = append(r.entries, cost.Entry{
		Session:  t.session,
		CWD:      cwd,
		At:       at,
		Model:    model,
		Tokens:   tokens,
		USD:      usd,
		Unpriced: !priced,
	})
}

func callKey(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:8])
}

func growth(now, before price.Tokens) price.Tokens {
	d := func(a, b int64) int64 { return max(a-b, 0) }
	return price.Tokens{
		Input:        d(now.Input, before.Input),
		Output:       d(now.Output, before.Output),
		CacheRead:    d(now.CacheRead, before.CacheRead),
		CacheWrite5m: d(now.CacheWrite5m, before.CacheWrite5m),
		CacheWrite1h: d(now.CacheWrite1h, before.CacheWrite1h),
		WebSearch:    d(now.WebSearch, before.WebSearch),
	}
}

// readHead fingerprints the start of a file, empty while the file is shorter
// than headSize: such a head still grows, so only full heads are compared.
func readHead(f io.ReaderAt) (string, error) {
	buf := make([]byte, headSize)
	n, err := f.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if n < headSize {
		return "", nil
	}
	sum := sha256.Sum256(buf[:n])
	return hex.EncodeToString(sum[:]), nil
}

// lineTime reads a timestamp off a line without parsing the line. A nested
// one, in a tool result claude stored, is a moment of the same session too,
// and only the newest is kept.
func lineTime(line []byte) (time.Time, bool) {
	i := bytes.Index(line, timeMarker)
	if i < 0 {
		return time.Time{}, false
	}
	rest := line[i+len(timeMarker):]
	end := bytes.IndexByte(rest, '"')
	if end < 0 {
		return time.Time{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, string(rest[:end]))
	return at, err == nil
}

func lineCWD(line []byte) string {
	if !bytes.Contains(line, cwdMarker) {
		return ""
	}
	var l struct {
		CWD string `json:"cwd"`
	}
	if json.Unmarshal(line, &l) != nil {
		return ""
	}
	return l.CWD
}
