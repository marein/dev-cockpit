package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A query goes as a POST of JSON to the API's address itself, with the token
// in its header alone, the query and its variables in the body; the data
// comes back, and an answer is never kept, so a query asks again even where
// the server sent an ETag.
func TestAQueryPostsJSONAndReadsTheData(t *testing.T) {
	type seen struct {
		method, path, contentType, auth, condition string
		body                                       map[string]any
	}
	var requests []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		s := seen{method: r.Method, path: r.URL.String(), contentType: r.Header.Get("Content-Type"), auth: r.Header.Get("Authorization"), condition: r.Header.Get("If-None-Match")}
		json.Unmarshal(raw, &s.body)
		requests = append(requests, s)
		w.Header().Set("ETag", `"1"`)
		writeJSON(w, `{"data":{"viewer":{"login":"octocat"}}}`)
	}))
	defer srv.Close()
	a := NewClient().API(srv.URL+"/api/graphql", Host{}, secret, nil)
	for range 2 {
		var data struct {
			Viewer struct {
				Login string `json:"login"`
			} `json:"viewer"`
		}
		if err := a.Query(context.Background(), "query($n: Int!) { viewer { login } }", map[string]any{"n": 3}, &data); err != nil || data.Viewer.Login != "octocat" {
			t.Fatalf("got %+v, %v", data, err)
		}
	}
	if len(requests) != 2 {
		t.Fatalf("the server was asked %d times, want 2", len(requests))
	}
	for _, r := range requests {
		if r.method != http.MethodPost || r.path != "/api/graphql" || r.contentType != "application/json" || r.auth != "Bearer "+secret || r.condition != "" {
			t.Fatalf("the request was %+v", r)
		}
		if r.body["query"] != "query($n: Int!) { viewer { login } }" || fmt.Sprint(r.body["variables"]) != "map[n:3]" {
			t.Fatalf("the body was %v", r.body)
		}
	}
}

// An answer that carries errors is the first one's type and message, the
// message cut and written visible, even beside data; one that is no JSON, or
// no answer of the API, is refused like a read's; a limit the server set
// holds the next query back.
func TestAQueryThatFailsIsWorded(t *testing.T) {
	var asked atomic.Int32
	now := time.Unix(1_800_000_000, 0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		switch r.URL.Path {
		case "/errors":
			writeJSON(w, `{"data":{"repository":null},"errors":[{"type":"NOT_FOUND","path":["repository"],"message":"Could not resolve to a Repository with the name 'octo/gone'."},{"type":"OTHER","message":"second"}]}`)
		case "/long":
			writeJSON(w, `{"errors":[{"message":"`+strings.Repeat("x", 300)+`"}]}`)
		case "/controls":
			writeJSON(w, `{"errors":[{"message":"\u001b[2Jgone\rover\nline"}]}`)
		case "/html":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html>sign in</html>")
		case "/refused":
			w.WriteHeader(http.StatusUnauthorized)
			writeJSON(w, `{"message":"Bad credentials"}`)
		case "/limited":
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()
	c := NewClient()
	c.now = func() time.Time { return now }
	query := func(path string) error {
		t.Helper()
		var data map[string]any
		err := c.API(srv.URL+path, Host{}, secret, nil).Query(context.Background(), "{ x }", nil, &data)
		if err != nil && strings.Contains(err.Error(), secret) {
			t.Fatalf("the error names the token: %v", err)
		}
		return err
	}
	var refused *GraphQLError
	if err := query("/errors"); !errors.As(err, &refused) || refused.Type != "NOT_FOUND" || err.Error() != "The server refused the query: Could not resolve to a Repository with the name 'octo/gone'." {
		t.Fatalf("errors beside data: %v", err)
	}
	if err := query("/long"); !errors.As(err, &refused) || refused.Type != "" || len([]rune(refused.Message)) != maxQuotedCause || !strings.HasSuffix(refused.Message, "…") {
		t.Fatalf("a long message: %v", err)
	}
	if err := query("/controls"); !errors.As(err, &refused) || refused.Message != `\x1b[2Jgone\rover\nline` {
		t.Fatalf("a message with control characters: %v", err)
	}
	if err := query("/html"); !errors.Is(err, ErrNotJSON) {
		t.Fatalf("a web page: %v", err)
	}
	var status *StatusError
	if err := query("/refused"); !errors.As(err, &status) || status.Code != http.StatusUnauthorized {
		t.Fatalf("a refused token: %v", err)
	}
	var limit *RateLimitError
	if err := query("/limited"); !errors.As(err, &limit) {
		t.Fatalf("a limit: %v", err)
	}
	before := asked.Load()
	if err := query("/limited"); !errors.As(err, &limit) || asked.Load() != before {
		t.Fatalf("the limit did not hold the query back: %v, asked %d times", err, asked.Load()-before)
	}
}
