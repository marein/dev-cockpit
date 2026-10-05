package price

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// feedRow is one row of the list in its own shape, the anthropic rows of
// which carry these fields.
const feedRow = `{"max_tokens":128000,"max_input_tokens":200000,"input_cost_per_token":5e-06,"output_cost_per_token":2.5e-05,` +
	`"cache_creation_input_token_cost":6.25e-06,"cache_creation_input_token_cost_above_1hr":1e-05,"cache_read_input_token_cost":5e-07,` +
	`"search_context_cost_per_query":{"search_context_size_low":0.01,"search_context_size_medium":0.01,"search_context_size_high":0.01},` +
	`"provider_specific_entry":{"us":1.1,"fast":2},"litellm_provider":"anthropic","mode":"chat","supports_vision":true}`

func checkTable(t *testing.T, what string, table Table) {
	t.Helper()
	max := Tokens{Input: math.MaxInt64, Output: math.MaxInt64, CacheRead: math.MaxInt64, CacheWrite5m: math.MaxInt64, CacheWrite1h: math.MaxInt64, WebSearch: math.MaxInt64}
	for provider, models := range table {
		for model, r := range models {
			if !r.plausible() {
				t.Fatalf("%s prices %s/%s implausibly: %+v", what, provider, model, r)
			}
			for _, fast := range []bool{false, true} {
				if c := r.Cost(max, fast); math.IsInf(c, 0) || math.IsNaN(c) || c < 0 {
					t.Fatalf("%s prices the largest call of %s/%s at %g", what, provider, model, c)
				}
			}
		}
	}
}

// FuzzFeed reads any list, alone and as one row among a list that prices
// every known model. Nothing panics, a parsed table prices every call
// finitely, and a list the book refuses leaves the last good table in place.
func FuzzFeed(f *testing.F) {
	log.SetOutput(io.Discard)
	f.Cleanup(func() { log.SetOutput(os.Stderr) })
	good := fullFeed(f)
	f.Add(good, "claude-opus-5-5", []byte(feedRow))
	f.Add([]byte(`{"claude-opus-5-5":`+feedRow+`,"sample_spec":{"litellm_provider":"x"}}`), "claude-new-6", []byte(feedRow))
	f.Add([]byte(`{"a":{"litellm_provider":"anthropic","input_cost_per_token":1e300,"output_cost_per_token":1,"cache_read_input_token_cost":1}}`), "claude-opus-5-5",
		[]byte(`{"litellm_provider":"anthropic","input_cost_per_token":-1,"output_cost_per_token":1,"cache_read_input_token_cost":1}`))
	f.Add([]byte(`{"a":{"litellm_provider":"anthropic","input_cost_per_token":"1","output_cost_per_token":null}}`), "claude-opus-5-5",
		[]byte(`{"litellm_provider":"anthropic","input_cost_per_token":0.0001,"output_cost_per_token":0.0001,"cache_read_input_token_cost":0.0001,"provider_specific_entry":{"fast":1e308}}`))
	f.Add([]byte(`[]`), "anthropic/claude-opus-5-5", []byte(`null`))
	f.Add([]byte(`{"x":`), "", []byte(`{"litellm_provider":"anthropic","input_cost_per_token":1e-6,"output_cost_per_token":1e-6,"cache_read_input_token_cost":1e-6,"search_context_cost_per_query":{"search_context_size_medium":1e300}}`))

	var body atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body.Load().([]byte))
	}))
	f.Cleanup(srv.Close)

	f.Fuzz(func(t *testing.T, list []byte, model string, row []byte) {
		if table, err := parseFeed(list); err == nil {
			checkTable(t, "the list", table)
		}
		var rows map[string]json.RawMessage
		if err := json.Unmarshal(good, &rows); err != nil {
			t.Fatal(err)
		}
		if json.Valid(row) {
			rows[model] = row
		}
		merged, err := json.Marshal(rows)
		if err != nil {
			t.Fatal(err)
		}

		c := &clock{now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}
		on := true
		b := newBook(t, t.TempDir(), srv.URL, c, &on)
		body.Store(good)
		b.Tick(context.Background())
		last := b.Table()
		for _, next := range [][]byte{merged, list} {
			body.Store(next)
			c.now = c.now.Add(refreshEvery)
			fetched := b.Status().FetchedAt
			b.Tick(context.Background())
			table := b.Table()
			checkTable(t, "the book", table)
			if b.Status().FetchedAt.Equal(fetched) && !reflect.DeepEqual(table, last) {
				t.Fatalf("a refused list changed the table")
			}
			last = table
		}
	})
}

// FuzzStoredTable starts a book over any stored prices.json. Nothing panics
// and the book prices every call finitely.
func FuzzStoredTable(f *testing.F) {
	log.SetOutput(io.Discard)
	f.Cleanup(func() { log.SetOutput(os.Stderr) })
	stored, _ := json.Marshal(cacheFile{
		FetchedAt: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC),
		NextAt:    time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC),
		Table:     Snapshot(),
	})
	f.Add(stored)
	f.Add([]byte(`{"table":{"anthropic":{"claude-opus-5-5":{"input":1e303,"output":1,"cache_read":1,"cache_write_5m":1,"cache_write_1h":1}}}}`))
	f.Add([]byte(`{"table":{"anthropic":{"claude-opus-5-5":{"input":-1,"output":1,"cache_read":1,"cache_write_5m":1,"cache_write_1h":1,"fast":-3}}}}`))
	f.Add([]byte(`{"table":{"anthropic":null,"x":{"m":null}},"next_at":"9999-12-31T23:59:59Z"}`))
	f.Add([]byte(`{"table":[1,2]}`))
	f.Add([]byte("\x00garbage"))
	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		path := filepath.Join(dir, "cost", "prices.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		b := NewBook(dir, func() bool { return false })
		checkTable(t, "the stored table", b.Table())
		if _, ok := b.Table().Lookup(Anthropic, "claude-opus-5-5"); !ok {
			t.Fatal("a stored table took a known model away")
		}
		b.Status()
	})
}

// FuzzOllamaPage reads any page, alone and through the book after the real
// page was taken. Nothing panics, a table the book takes prices every call
// finitely, and a page it refuses leaves the last good table in place.
func FuzzOllamaPage(f *testing.F) {
	log.SetOutput(io.Discard)
	f.Cleanup(func() { log.SetOutput(os.Stderr) })
	good := ollamaPage(f)
	page := string(good)
	f.Add(good)
	f.Add([]byte(page[:len(page)/2]))
	f.Add([]byte(strings.ReplaceAll(page, ">$0.14<", ">$1e308<")))
	f.Add([]byte(strings.ReplaceAll(page, ">$0.14<", ">NaN<")))
	f.Add([]byte(strings.ReplaceAll(page, ">$3.00<", ">-<")))
	f.Add([]byte(strings.ReplaceAll(page, "</td>", "")))
	f.Add([]byte(`<table><tr><th>Model</th><th>Input</th><th>Cached input</th><th>Output</th></tr><tr><td>a (Off-Peak)</td><td>1</td><td>-</td><td>2</td></tr></table>`))
	f.Add([]byte("<table><tr><td>"))

	var body atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body.Load().([]byte))
	}))
	f.Cleanup(srv.Close)

	f.Fuzz(func(t *testing.T, data []byte) {
		if rates, err := parseOllama(data); err == nil {
			if _, err := acceptOllama(rates, Snapshot()[Ollama], nil, nil); err == nil {
				checkTable(t, "the page", Table{Ollama: rates})
			}
		}
		c := &clock{now: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
		b := NewBook(t.TempDir(), func() bool { return true })
		b.url, b.ollamaURL, b.now = "http://127.0.0.1:0/", srv.URL, c.Now
		b.cache.NextAt = c.now.Add(100 * 365 * 24 * time.Hour)
		body.Store(good)
		b.Tick(context.Background())
		last := b.Table()
		body.Store(data)
		c.now = c.now.Add(refreshEvery)
		fetched := b.Status().Ollama.FetchedAt
		b.Tick(context.Background())
		table := b.Table()
		checkTable(t, "the book", table)
		if b.Status().Ollama.FetchedAt.Equal(fetched) && !reflect.DeepEqual(table, last) {
			t.Fatal("a refused page changed the table")
		}
		if _, ok := table.Lookup(Ollama, "nemotron-3-ultra"); !ok {
			t.Fatal("a page took a known model away")
		}
	})
}
