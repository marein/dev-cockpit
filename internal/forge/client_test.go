package forge

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const secret = "glpat-s3cr3t-t0ken-n3ver-sh0wn"

func get(t *testing.T, c *Client, base string, h Host) (map[string]any, http.Header, error) {
	t.Helper()
	var v map[string]any
	header, err := c.API(base, h, secret, nil).Get(context.Background(), "/thing", &v)
	if err != nil && strings.Contains(err.Error(), secret) {
		t.Fatalf("the error names the token: %v", err)
	}
	return v, header, err
}

func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	fmt.Fprint(w, body)
}

// The token travels in the Authorization header alone, never in the address.
func TestTheTokenGoesInAHeaderAlone(t *testing.T) {
	var seen *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r
		writeJSON(w, `{"ok":true}`)
	}))
	defer srv.Close()
	v, _, err := get(t, NewClient(), srv.URL+"/api/v4/", Host{})
	if err != nil || v["ok"] != true {
		t.Fatalf("got %v, %v", v, err)
	}
	if seen.URL.Path != "/api/v4/thing" || seen.URL.RawQuery != "" || strings.Contains(seen.URL.String(), secret) {
		t.Fatalf("the request went to %s", seen.URL)
	}
	if got := seen.Header.Get("Authorization"); got != "Bearer "+secret {
		t.Fatalf("Authorization %q", got)
	}
	if seen.Header.Get("User-Agent") != "dev-cockpit" || seen.Header.Get("Accept") != "application/json" {
		t.Fatalf("headers %v", seen.Header)
	}
}

// A redirect is answered, never followed: the token never reaches the
// address it points to, and the sentence names it without its query.
func TestARedirectIsNeverFollowed(t *testing.T) {
	var reached atomic.Bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Store(true)
		writeJSON(w, `{}`)
	}))
	defer elsewhere.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/api/v4/thing?private_token=x#f", http.StatusMovedPermanently)
	}))
	defer srv.Close()
	_, _, err := get(t, NewClient(), srv.URL, Host{})
	want := "The server redirects to " + elsewhere.URL + "/api/v4/thing, which is not followed: check the web address."
	if err == nil || err.Error() != want {
		t.Fatalf("got %v, want %q", err, want)
	}
	if reached.Load() {
		t.Fatal("the redirect was followed")
	}
}

func TestAServerThatDoesNotAnswerInTimeIsLeft(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	var v any
	_, err := NewClient().API(srv.URL, Host{}, secret, nil).Get(ctx, "/thing", &v)
	if err == nil || err.Error() != "The server did not answer in time." {
		t.Fatalf("got %v", err)
	}
}

func certificatePEM(srv *httptest.Server) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))
}

// A server signed by an authority the host does not know is refused unless
// the host's CA bundle carries it, and the name must match either way.
func TestTLSTrustsTheSystemAndTheHostsCABundleAlone(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, `{"ok":true}`) }))
	defer srv.Close()
	c := NewClient()
	_, _, err := get(t, c, srv.URL, Host{})
	if err == nil || !strings.HasPrefix(err.Error(), "The server's certificate is signed by an unknown authority,") {
		t.Fatalf("an unknown authority: %v", err)
	}
	trusted := Host{CABundle: certificatePEM(srv)}
	if v, _, err := get(t, c, srv.URL, trusted); err != nil || v["ok"] != true {
		t.Fatalf("with the CA bundle: %v, %v", v, err)
	}
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "https://"))
	_, _, err = get(t, c, "https://localhost:"+port, trusted)
	if err == nil || err.Error() != "The server's certificate is not valid for localhost." {
		t.Fatalf("a name the certificate does not hold: %v", err)
	}
	if _, _, err := get(t, c, srv.URL, Host{CABundle: "no certificate"}); err == nil || err.Error() != "The host's CA bundle holds no certificate." {
		t.Fatalf("a bundle without a certificate: %v", err)
	}
}

func TestAServerWithoutTLSIsNamedSo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, `{}`) }))
	defer srv.Close()
	_, _, err := get(t, NewClient(), strings.Replace(srv.URL, "http://", "https://", 1), Host{})
	if err == nil || err.Error() != "The server does not answer with TLS here, its web address may start with http://." {
		t.Fatalf("got %v", err)
	}
}

func TestUnreachableServersAreWorded(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	cases := map[string]string{
		closed.URL:                    "The server refuses the connection.",
		"http://169.254.169.254":      "The server's address is link local, which is refused.",
		"http://[fe80::1%25lo]:80":    "The server's address is link local, which is refused.",
		"http://nothing-here.invalid": "The name nothing-here.invalid does not resolve.",
		"http://exa mple.com":         "The host's API address is invalid.",
	}
	for base, want := range cases {
		if _, _, err := get(t, NewClient(), base, Host{}); err == nil || err.Error() != want {
			t.Errorf("%s: got %v, want %q", base, err, want)
		}
	}
}

func TestAnswersOutsideTheJSONOfAnAPIAreRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page/thing":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<html>sign in</html>")
		case "/broken/thing":
			writeJSON(w, `{"half":`)
		case "/huge/thing":
			writeJSON(w, `{"pad":"`+strings.Repeat("x", maxAnswer)+`"}`)
		case "/vendor/thing":
			w.Header().Set("Content-Type", "application/vnd.github+json")
			fmt.Fprint(w, `{"ok":true}`)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprint(w, `{"message":"upstream"}`)
		}
	}))
	defer srv.Close()
	c := NewClient()
	for path, want := range map[string]error{"/page": ErrNotJSON, "/broken": ErrNotJSON} {
		if _, _, err := get(t, c, srv.URL+path, Host{}); !errors.Is(err, want) {
			t.Errorf("%s: %v", path, err)
		}
	}
	if _, _, err := get(t, c, srv.URL+"/huge", Host{}); err == nil || err.Error() != "The server's answer is larger than 2 MiB." {
		t.Errorf("a huge answer: %v", err)
	}
	if v, _, err := get(t, c, srv.URL+"/vendor", Host{}); err != nil || v["ok"] != true {
		t.Errorf("a vendor JSON answer: %v, %v", v, err)
	}
	_, _, err := get(t, c, srv.URL+"/fail", Host{})
	var status *StatusError
	if !errors.As(err, &status) || status.Code != http.StatusBadGateway || string(status.Body) != `{"message":"upstream"}` || err.Error() != "The server answered 502 Bad Gateway." {
		t.Errorf("a failure: %v", err)
	}
}

// A refused request holds every further one to the same API back until the
// limit lifts, an answer that used the limit up holds the next ones, and
// another API is not held by it.
func TestARateLimitHoldsTheRequestsBack(t *testing.T) {
	var asked atomic.Int32
	now := time.Unix(1_800_000_000, 0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked.Add(1)
		switch r.URL.Path {
		case "/secondary/thing":
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusForbidden)
		case "/primary/thing":
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", fmt.Sprint(now.Add(5*time.Minute).Unix()))
			w.WriteHeader(http.StatusForbidden)
		case "/gitlab/thing":
			w.Header().Set("RateLimit-Remaining", "0")
			w.Header().Set("RateLimit-Reset", fmt.Sprint(now.Add(10*time.Second).Unix()))
			writeJSON(w, `{"ok":true}`)
		case "/plain/thing":
			w.WriteHeader(http.StatusTooManyRequests)
		case "/forbidden/thing":
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()
	c := NewClient()
	c.now = func() time.Time { return now }
	wantLimit := func(path, says string, wantAsked int32) {
		t.Helper()
		_, _, err := get(t, c, srv.URL+path, Host{})
		var limit *RateLimitError
		if !errors.As(err, &limit) || err.Error() != says {
			t.Fatalf("%s: %v, want %q", path, err, says)
		}
		if asked.Load() != wantAsked {
			t.Fatalf("%s: the server was asked %d times, want %d", path, asked.Load(), wantAsked)
		}
	}
	wantLimit("/secondary", "The server limits the requests, try again in 30 seconds.", 1)
	wantLimit("/secondary", "The server limits the requests, try again in 30 seconds.", 1)
	wantLimit("/primary", "The server limits the requests, try again in 5 minutes.", 2)
	now = now.Add(31 * time.Second)
	wantLimit("/secondary", "The server limits the requests, try again in 30 seconds.", 3)
	wantLimit("/primary", "The server limits the requests, try again in 5 minutes.", 3)
	if v, _, err := get(t, c, srv.URL+"/gitlab", Host{}); err != nil || v["ok"] != true {
		t.Fatalf("the answer that used the limit up: %v, %v", v, err)
	}
	wantLimit("/gitlab", "The server limits the requests, try again in 10 seconds.", 4)
	wantLimit("/plain", "The server limits the requests, try again in 60 seconds.", 5)
	if _, _, err := get(t, c, srv.URL+"/forbidden", Host{}); err == nil || err.Error() != "The server answered 403 Forbidden." {
		t.Fatalf("a plain 403: %v", err)
	}
}

// An answer with an ETag is asked for again on condition and stands in for
// a 304, header included; a changed answer replaces it, another token keeps
// its own, and an answer without ETag is asked for plainly.
func TestAnAnswerWithAnETagIsAskedForOnCondition(t *testing.T) {
	var conditions []string
	body := `{"n":1}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conditions = append(conditions, r.Header.Get("If-None-Match"))
		etag := fmt.Sprintf(`"%x"`, len(body))
		if r.URL.Path == "/plain/thing" {
			writeJSON(w, body)
			return
		}
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("X-Next-Page", "2")
		writeJSON(w, body)
	}))
	defer srv.Close()
	c := NewClient()
	read := func(token, base string) (map[string]any, http.Header) {
		t.Helper()
		var v map[string]any
		header, err := c.API(base, Host{}, token, nil).Get(context.Background(), "/thing", &v)
		if err != nil {
			t.Fatal(err)
		}
		return v, header
	}
	read(secret, srv.URL)
	v, header := read(secret, srv.URL)
	if v["n"] != 1.0 || header.Get("X-Next-Page") != "2" {
		t.Fatalf("the kept answer reads %v with %v", v, header)
	}
	body = `{"n":22}`
	if v, _ := read(secret, srv.URL); v["n"] != 22.0 {
		t.Fatalf("a changed answer reads %v", v)
	}
	read(secret, srv.URL)
	read(secret+"2", srv.URL)
	read(secret, srv.URL+"/plain")
	read(secret, srv.URL+"/plain")
	want := []string{"", `"7"`, `"7"`, `"8"`, "", "", ""}
	if !slices.Equal(conditions, want) {
		t.Fatalf("the conditions sent: %q, want %q", conditions, want)
	}
}

// A write is a POST without a body that carries the token in its header
// alone, is never asked on condition, and words a refusal with the
// forge's message, cut and with its control characters written visible.
func TestAPostActsOnceAndWordsItsRefusal(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization")+" "+r.Header.Get("If-None-Match")+fmt.Sprint(r.ContentLength))
		if r.URL.Path == "/api/refused" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintf(w, `{"message":"403 Forbidden - Job is not retryable %s"}`, strings.Repeat("x", 200))
			return
		}
		w.Header().Set("ETag", `"v1"`)
		writeJSON(w, `{"id":12}`)
	}))
	defer srv.Close()
	a := NewClient().API(srv.URL+"/api", Host{}, secret, nil)
	for range 2 {
		if err := a.Post(context.Background(), "/retry", nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	err := a.Post(context.Background(), "/refused", nil, nil)
	var status *StatusError
	if !errors.As(err, &status) || status.Code != http.StatusForbidden || !strings.HasPrefix(status.Message(), "403 Forbidden - Job is not retryable x") || len([]rune(status.Message())) != maxQuotedCause {
		t.Fatalf("the refusal reads %v, %q", err, status.Message())
	}
	want := "POST /api/retry Bearer " + secret + " 0"
	if len(seen) != 3 || seen[0] != want || seen[1] != want {
		t.Fatalf("the server saw %q", seen)
	}
	if (&StatusError{Body: []byte("<html>")}).Message() != "" {
		t.Fatal("a body without JSON has a message")
	}
	controls := &StatusError{Code: http.StatusConflict, Body: []byte(`{"message":"\u001b]0;owned\u0007\u001b[2J\u001b[1;31mFAKE $ \u001b[0m\rline2\nline3\u202e "}`)}
	if got, want := controls.Message(), `\x1b]0;owned\a\x1b[2J\x1b[1;31mFAKE $ \x1b[0m\rline2\nline3\u202e`; got != want {
		t.Fatalf("a message with control characters reads %q, want %q", got, want)
	}
}

// A write with a body sends it as JSON and reads the JSON answer, which is
// never kept.
func TestAPostSendsItsBodyAndReadsItsAnswer(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("Content-Type")+" "+string(body))
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"iid":5,"title":"Draft: Fix"}`)
	}))
	defer srv.Close()
	a := NewClient().API(srv.URL+"/api", Host{}, secret, nil)
	var answer struct {
		IID   int    `json:"iid"`
		Title string `json:"title"`
	}
	for range 2 {
		if err := a.Post(context.Background(), "/merge_requests", map[string]any{"title": "Fix", "draft": true}, &answer); err != nil || answer.IID != 5 || answer.Title != "Draft: Fix" {
			t.Fatalf("the answer reads %+v, %v", answer, err)
		}
	}
	want := `POST /api/merge_requests application/json {"draft":true,"title":"Fix"}`
	if len(seen) != 2 || seen[0] != want || seen[1] != want {
		t.Fatalf("the server saw %q", seen)
	}
}

// A refusal's message reads the way each forge words it: one sentence, a
// list, the sentences of each field, or GitHub's errors by message or else by
// field and code.
func TestARefusalsMessageReadsEveryShape(t *testing.T) {
	for _, c := range []struct {
		body, want string
	}{
		{`{"message":"404 Project Not Found"}`, "404 Project Not Found"},
		{`{"message":["Another open merge request already exists for this source branch: !1"]}`, "Another open merge request already exists for this source branch: !1"},
		{`{"message":{"title":["is too long (maximum is 255 characters)"],"target_branch":["does not exist"]}}`, "target_branch does not exist; title is too long (maximum is 255 characters)"},
		{`{"message":"Validation Failed","errors":[{"resource":"PullRequest","code":"custom","message":"A pull request already exists for octo:feature."}]}`, "A pull request already exists for octo:feature."},
		{`{"message":"Validation Failed","errors":[{"resource":"PullRequest","field":"base","code":"invalid"}]}`, "base: invalid"},
		{`{"message":"Validation Failed","errors":[{"resource":"PullRequest"}]}`, "Validation Failed"},
		{`{"message":["a\u001b[2J"]}`, `a\x1b[2J`},
		{`{"error":"title is missing"}`, ""},
	} {
		if got := (&StatusError{Code: http.StatusUnprocessableEntity, Body: []byte(c.body)}).Message(); got != c.want {
			t.Errorf("%s reads %q, want %q", c.body, got, c.want)
		}
	}
}

// A server that quotes the request back, the Authorization header and all,
// shows the token nowhere: a refusal's message, an answer, a GraphQL error, a
// log and a redirect read it cut out.
func TestAnAnswerQuotingTheTokenReadsItCutOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/api/refused":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			fmt.Fprintf(w, `{"message":"bad header %s"}`, auth)
		case "/api/thing":
			writeJSON(w, fmt.Sprintf(`{"title":"%s"}`, auth))
		case "/api/graphql":
			writeJSON(w, fmt.Sprintf(`{"errors":[{"type":"FORBIDDEN","message":"no access for %s"}]}`, auth))
		case "/api/log":
			fmt.Fprintf(w, "line 1\nexport TOKEN=%s\n", auth)
		case "/api/moved":
			http.Redirect(w, r, "/elsewhere/"+strings.TrimPrefix(auth, "Bearer "), http.StatusMovedPermanently)
		}
	}))
	defer srv.Close()
	a := NewClient().API(srv.URL+"/api", Host{}, secret, nil)
	ctx := context.Background()
	var status *StatusError
	if err := a.Post(ctx, "/refused", nil, nil); !errors.As(err, &status) || status.Message() != "bad header Bearer [token]" || strings.Contains(string(status.Body), secret) {
		t.Fatalf("the refusal reads %v", err)
	}
	var thing struct {
		Title string `json:"title"`
	}
	if _, err := a.Get(ctx, "/thing", &thing); err != nil || thing.Title != "Bearer [token]" {
		t.Fatalf("the answer reads %+v, %v", thing, err)
	}
	if err := NewClient().API(srv.URL+"/api/graphql", Host{}, secret, nil).Query(ctx, "{}", nil, &thing); err == nil || err.Error() != "The server refused the query: no access for Bearer [token]." {
		t.Fatalf("the query reads %v", err)
	}
	if log, _, err := a.Tail(ctx, "/log", 1<<10); err != nil || string(log) != "line 1\nexport TOKEN=Bearer [token]\n" {
		t.Fatalf("the log reads %q, %v", log, err)
	}
	if _, err := a.Get(ctx, "/moved", &thing); err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "/elsewhere/[token]") {
		t.Fatalf("the redirect reads %v", err)
	}
}
