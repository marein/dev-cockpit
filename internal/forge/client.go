package forge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/marein/dev-cockpit/internal/netguard"
	"github.com/marein/dev-cockpit/internal/plaintext"
)

// The bounds every forge request stands in.
const (
	requestTimeout   = 15 * time.Second
	dialTimeout      = 5 * time.Second
	handshakeTimeout = 10 * time.Second
	maxAnswer        = 2 << 20
	maxErrorAnswer   = 16 << 10
	logTimeout       = 30 * time.Second
	maxLog           = 32 << 20
	defaultHold      = time.Minute
	maxHold          = time.Hour
	maxTransports    = 64
	maxQuotedCause   = 120
	maxKept          = 256
	maxKeptBytes     = 8 << 20
)

// ErrNotJSON answers an answer that is no JSON, what a server that is no
// forge's API sends: a web page, a sign in, a proxy's error.
var ErrNotJSON = errors.New("The server's answer is no JSON.")

var errLinkLocal = errors.New("link local address refused")

// StatusError is an answer outside 2xx and 3xx that no rule of the door
// takes, for the provider to say what it means. Body is its start.
type StatusError struct {
	Code int
	Body []byte
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("The server answered %d %s.", e.Code, http.StatusText(e.Code))
}

// Message is the message a forge words a refusal with, as forgeSaid quotes
// it, "" without one: the field message of its JSON body, which GitLab also
// answers as a list of sentences or as the sentences of each field it
// refused, or in place of it the errors GitHub lists, each by its message or
// else by its field and code.
func (e *StatusError) Message() string {
	var answer struct {
		Message json.RawMessage `json:"message"`
		Errors  []struct {
			Field   string `json:"field"`
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(e.Body, &answer) != nil {
		return ""
	}
	var said []string
	for _, item := range answer.Errors {
		switch {
		case strings.TrimSpace(item.Message) != "":
			said = append(said, item.Message)
		case item.Field != "" && item.Code != "":
			said = append(said, item.Field+": "+item.Code)
		}
	}
	if len(said) == 0 {
		said = messages(answer.Message)
	}
	return forgeSaid(strings.Join(said, "; "))
}

// messages reads the field message of a refusal: one sentence, a list of
// them, or the sentences of each field, each then named before its own.
func messages(raw json.RawMessage) []string {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var fields map[string][]string
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	var said []string
	for _, field := range slices.Sorted(maps.Keys(fields)) {
		for _, sentence := range fields[field] {
			said = append(said, field+" "+sentence)
		}
	}
	return said
}

// forgeSaid is what a forge wrote in its own words, the way a sentence of
// the door quotes it: cut, and written visible, since the sentence reaches a
// terminal and a thread, where a control character the forge sent would act
// or hide.
func forgeSaid(s string) string {
	return quotedCut(plaintext.Visible(strings.TrimSpace(s)), maxQuotedCause)
}

// RateLimitError answers a request the server limited, or one the door held
// back because the server's limit has not lifted yet.
type RateLimitError struct {
	Wait time.Duration
}

func (e *RateLimitError) Error() string {
	if e.Wait <= 90*time.Second {
		return fmt.Sprintf("The server limits the requests, try again in %d seconds.", max(1, int((e.Wait+time.Second-1)/time.Second)))
	}
	return fmt.Sprintf("The server limits the requests, try again in %d minutes.", int((e.Wait+time.Minute-1)/time.Minute))
}

// Client is the one door every forge request goes through. It dials past
// netguard, never follows a redirect, bounds the time and the size of an
// answer, carries the token in a header alone, words every failure without
// it and cuts it out of every answer, and holds back from a server that
// limits its requests until the limit lifts. It keeps the JSON answers that
// carry an ETag and asks for them again on condition: a poll that finds
// nothing changed costs GitHub's rate limit nothing.
type Client struct {
	now func() time.Time

	mu         sync.Mutex
	transports map[[sha256.Size]byte]*http.Transport
	holds      map[string]time.Time
	kept       map[[sha256.Size]byte]*keptAnswer
	keptBytes  int
	keptUse    uint64
}

// keptAnswer is an answer the door asks for again on condition, by its ETag.
type keptAnswer struct {
	etag   string
	header http.Header
	body   []byte
	used   uint64
}

// NewClient is a door without a request out yet.
func NewClient() *Client {
	return &Client{now: time.Now, transports: map[[sha256.Size]byte]*http.Transport{}, holds: map[string]time.Time{}, kept: map[[sha256.Size]byte]*keptAnswer{}}
}

// API is the door bound to one API of a host and the token it is read with.
// Header carries what the provider sends with every request.
type API struct {
	client *Client
	base   string
	host   Host
	token  string
	header http.Header
}

// API binds the door to the API at base, on the host whose CA bundle the
// connection trusts, read with the token.
func (c *Client) API(base string, h Host, token string, header http.Header) API {
	return API{client: c, base: strings.TrimRight(base, "/"), host: h, token: token, header: header}
}

// Base is the API's address for a provider's sentence about it, cut like
// every address the door quotes: the web address it stands below may be long
// enough to burst what a sentence rides on.
func (a API) Base() string {
	return quotedCut(a.base, maxQuotedCause)
}

// Get reads the JSON answer at path below the API into v and answers the
// answer's header. An answer kept by its ETag is asked for on condition and
// stands in for one the server did not change.
func (a API) Get(ctx context.Context, path string, v any) (http.Header, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	key := sha256.Sum256([]byte(a.token + "\x00" + a.base + path))
	kept := a.client.conditional(key)
	etag := ""
	if kept != nil {
		etag = kept.etag
	}
	resp, err := a.send(ctx, http.MethodGet, path, nil, "application/json", etag)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified && kept != nil {
		if err := json.Unmarshal(kept.body, v); err != nil {
			return kept.header, ErrNotJSON
		}
		return kept.header, nil
	}
	data, err := a.jsonBody(resp)
	if err != nil {
		return resp.Header, err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return resp.Header, ErrNotJSON
	}
	if etag := resp.Header.Get("ETag"); etag != "" {
		a.client.keep(key, etag, resp.Header, data)
	}
	return resp.Header, nil
}

// GraphQLError is the first error a GraphQL API answered in place of the
// data asked for, its type as the server words it and its message as
// forgeSaid quotes it.
type GraphQLError struct {
	Type    string
	Message string
}

func (e *GraphQLError) Error() string {
	return fmt.Sprintf("The server refused the query: %s.", strings.TrimRight(e.Message, "."))
}

// Query asks the GraphQL API at the API's base for query with its variables
// and reads the data it answers into v, a mutation's alike. An answer to a
// POST is never kept, so every query asks the server again. An answer that
// carries errors is a GraphQLError of the first one.
func (a API) Query(ctx context.Context, query string, variables map[string]any, v any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	body, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return err
	}
	resp, err := a.send(ctx, http.MethodPost, "", body, "application/json", "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := a.jsonBody(resp)
	if err != nil {
		return err
	}
	var answer struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(data, &answer); err != nil {
		return ErrNotJSON
	}
	if len(answer.Errors) > 0 {
		return &GraphQLError{Type: answer.Errors[0].Type, Message: forgeSaid(answer.Errors[0].Message)}
	}
	if err := json.Unmarshal(answer.Data, v); err != nil {
		return ErrNotJSON
	}
	return nil
}

// Post asks the API at path to act on what it names, with body as JSON where
// there is one, and reads the JSON answer into v where there is one, else no
// more of it than whether it went through. It is never asked on condition and
// its answer is never kept.
func (a API) Post(ctx context.Context, path string, body, v any) error {
	return a.write(ctx, http.MethodPost, path, body, v)
}

// Put is Post for a change of what path names.
func (a API) Put(ctx context.Context, path string, body, v any) error {
	return a.write(ctx, http.MethodPut, path, body, v)
}

func (a API) write(ctx context.Context, method, path string, body, v any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	resp, err := a.send(ctx, method, path, payload, "application/json", "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if v == nil {
		return a.answered(resp)
	}
	data, err := a.jsonBody(resp)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return ErrNotJSON
	}
	return nil
}

// jsonBody reads an answer that went through as JSON within the size an
// answer may have.
func (a API) jsonBody(resp *http.Response) ([]byte, error) {
	if err := a.answered(resp); err != nil {
		return nil, err
	}
	if !jsonAnswer(resp.Header.Get("Content-Type")) {
		return nil, ErrNotJSON
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer+1))
	if err != nil {
		return nil, reachError(err)
	}
	if len(data) > maxAnswer {
		return nil, fmt.Errorf("The server's answer is larger than %d MiB.", maxAnswer>>20)
	}
	return a.unsaid(data), nil
}

// Tail reads the log at path below the API and keeps its last keep bytes,
// reporting whether more came before them.
func (a API) Tail(ctx context.Context, path string, keep int) ([]byte, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, logTimeout)
	defer cancel()
	resp, err := a.send(ctx, http.MethodGet, path, nil, "text/plain", "")
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if err := a.answered(resp); err != nil {
		return nil, false, err
	}
	return a.tail(resp, keep)
}

// Redirect asks path below the API for the address it hands a download out
// at, which it answers with a temporary redirect. The door does not follow
// it, TailAt reads the address as a request of its own. A permanent redirect
// moves what path names and is a RedirectError like any other.
func (a API) Redirect(ctx context.Context, path string) (*url.URL, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	resp, err := a.send(ctx, http.MethodGet, path, nil, "", "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect:
		location := resp.Header.Get("Location")
		if target, err := resp.Request.URL.Parse(location); err == nil && location != "" {
			return target, nil
		}
	}
	if err := a.answered(resp); err != nil {
		return nil, err
	}
	return nil, errors.New("The server answered without a download address.")
}

// TailAt reads the log at a download address an API handed out and keeps its
// last keep bytes. It goes as a request of its own past the same dialer and
// CA bundle, without the token and the provider's headers, and follows no
// redirect. An address that is not https, unless the API itself is plain
// http, is not read.
func (a API) TailAt(ctx context.Context, target *url.URL, keep int) ([]byte, bool, error) {
	if target.Scheme != "https" && (target.Scheme != "http" || !strings.HasPrefix(a.base, "http://")) {
		return nil, false, errors.New("The server's download address is not https.")
	}
	transport, err := a.client.transport(a.host.CABundle)
	if err != nil {
		return nil, false, err
	}
	ctx, cancel := context.WithTimeout(ctx, logTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, false, errors.New("The server's download address is invalid.")
	}
	req.Header.Set("User-Agent", "dev-cockpit")
	resp, err := unfollowed(transport).Do(req)
	if err != nil {
		return nil, false, reachError(err)
	}
	defer resp.Body.Close()
	if err := a.answered(resp); err != nil {
		return nil, false, err
	}
	return a.tail(resp, keep)
}

// send makes one request of the API, unless a limit the server set still
// holds: with the provider's headers, accept where they name no Accept, the
// condition of etag where there is one, body as JSON where there is one, and
// the token, and with the limit its answer carries recorded.
func (a API) send(ctx context.Context, method, path string, body []byte, accept, etag string) (*http.Response, error) {
	if wait, held := a.client.held(a.base); held {
		return nil, &RateLimitError{Wait: wait}
	}
	transport, err := a.client.transport(a.host.CABundle)
	if err != nil {
		return nil, err
	}
	var payload io.Reader
	if body != nil {
		payload = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, payload)
	if err != nil {
		return nil, errors.New("The host's API address is invalid.")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for name, values := range a.header {
		req.Header[name] = append([]string(nil), values...)
	}
	if req.Header.Get("Accept") == "" && accept != "" {
		req.Header.Set("Accept", accept)
	}
	req.Header.Set("User-Agent", "dev-cockpit")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if a.token != "" {
		req.Header.Set("Authorization", "Bearer "+a.token)
	}
	resp, err := unfollowed(transport).Do(req)
	if err != nil {
		return nil, reachError(err)
	}
	if wait, limited := a.client.limit(a.base, resp); limited {
		resp.Body.Close()
		return nil, &RateLimitError{Wait: wait}
	}
	return resp, nil
}

func unfollowed(transport *http.Transport) *http.Client {
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// answered is the error an answer outside 2xx stands for: a redirect, which
// is not followed, or a StatusError with the start of its body.
func (a API) answered(resp *http.Response) error {
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		redirect := redirectError(resp)
		redirect.Target = string(a.unsaid([]byte(redirect.Target)))
		return redirect
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorAnswer))
		return &StatusError{Code: resp.StatusCode, Body: a.unsaid(body)}
	}
	return nil
}

// unsaid is what the server answered without the token the door sent: one
// that quotes the request back, a proxy's error page or a debug echo, would
// show it to whoever reads the sentence, the thread of an approval among
// them.
func (a API) unsaid(body []byte) []byte {
	if a.token == "" {
		return body
	}
	return bytes.ReplaceAll(body, []byte(a.token), []byte("[token]"))
}

func (a API) tail(resp *http.Response, keep int) ([]byte, bool, error) {
	kept, cut, err := tail(resp, keep)
	return a.unsaid(kept), cut, err
}

// tail reads a log answer through to its end and keeps its last keep bytes,
// reporting whether more came before them. A web page is no log, and a log
// longer than the door reads is refused rather than cut short of its end,
// where a failure says why.
func tail(resp *http.Response, keep int) ([]byte, bool, error) {
	if kind, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type")); kind == "text/html" {
		return nil, false, errors.New("The server's answer is a web page, no log.")
	}
	var kept []byte
	cut := false
	chunk := make([]byte, 32<<10)
	for read := 0; ; {
		n, err := resp.Body.Read(chunk)
		if read += n; read > maxLog {
			return nil, false, fmt.Errorf("The log is larger than %d MiB, its end is not read.", maxLog>>20)
		}
		kept = append(kept, chunk[:n]...)
		if len(kept) > 2*keep {
			kept = kept[:copy(kept, kept[len(kept)-keep:])]
			cut = true
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, false, reachError(err)
		}
	}
	if len(kept) > keep {
		kept, cut = kept[len(kept)-keep:], true
	}
	return kept, cut, nil
}

func jsonAnswer(contentType string) bool {
	kind, _, err := mime.ParseMediaType(contentType)
	return err == nil && (kind == "application/json" || strings.HasSuffix(kind, "+json"))
}

// transport is the connection pool of one CA bundle, the system's roots
// alone for a host without one. A bundle that went out of use stays until
// the pools are many and all of them start over.
func (c *Client) transport(bundle string) (*http.Transport, error) {
	key := sha256.Sum256([]byte(bundle))
	c.mu.Lock()
	defer c.mu.Unlock()
	if t, ok := c.transports[key]; ok {
		return t, nil
	}
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if bundle != "" {
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM([]byte(bundle)) {
			return nil, errors.New("The host's CA bundle holds no certificate.")
		}
		config.RootCAs = pool
	}
	if len(c.transports) >= maxTransports {
		for _, t := range c.transports {
			t.CloseIdleConnections()
		}
		clear(c.transports)
	}
	t := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: dialTimeout, Control: refuseLinkLocal}).DialContext,
		TLSClientConfig:       config,
		TLSHandshakeTimeout:   handshakeTimeout,
		ResponseHeaderTimeout: requestTimeout,
		ForceAttemptHTTP2:     true,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       90 * time.Second,
	}
	c.transports[key] = t
	return t, nil
}

func refuseLinkLocal(network, address string, raw syscall.RawConn) error {
	if err := netguard.RefuseLinkLocal(network, address, raw); err != nil {
		return fmt.Errorf("%w: %v", errLinkLocal, err)
	}
	return nil
}

// conditional answers the answer kept under key, nil when there is none.
func (c *Client) conditional(key [sha256.Size]byte) *keptAnswer {
	c.mu.Lock()
	defer c.mu.Unlock()
	k, ok := c.kept[key]
	if !ok {
		return nil
	}
	c.keptUse++
	k.used = c.keptUse
	return k
}

// keep holds an answer by its ETag, within maxKept answers and maxKeptBytes,
// the ones used longest ago going first. One larger than a tenth of the
// bytes is not kept.
func (c *Client) keep(key [sha256.Size]byte, etag string, header http.Header, body []byte) {
	if len(body) > maxKeptBytes/10 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.kept[key]; ok {
		c.keptBytes -= len(old.body)
		delete(c.kept, key)
	}
	for len(c.kept) >= maxKept || c.keptBytes+len(body) > maxKeptBytes {
		var oldest [sha256.Size]byte
		first := true
		for k, v := range c.kept {
			if first || v.used < c.kept[oldest].used {
				oldest, first = k, false
			}
		}
		c.keptBytes -= len(c.kept[oldest].body)
		delete(c.kept, oldest)
	}
	c.keptUse++
	c.kept[key] = &keptAnswer{etag: etag, header: header.Clone(), body: body, used: c.keptUse}
	c.keptBytes += len(body)
}

// held reports how long a limit the server set still holds for its API.
func (c *Client) held(base string) (time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	until, ok := c.holds[base]
	if !ok {
		return 0, false
	}
	wait := until.Sub(c.now())
	if wait <= 0 {
		delete(c.holds, base)
		return 0, false
	}
	return wait, true
}

// limit reads the rate limit an answer carries: GitHub's X-RateLimit-*,
// GitLab's RateLimit-* and Retry-After of both. A refused request holds every
// further one back until the limit lifts, a limit used up by an answer that
// came through holds back the next ones. It reports whether this answer was
// refused for the limit, with the wait.
func (c *Client) limit(base string, resp *http.Response) (time.Duration, bool) {
	now := c.now()
	retry, retrySet := retryAfter(resp.Header.Get("Retry-After"), now)
	reset, resetSet := firstHeader(resp.Header, "X-RateLimit-Reset", "RateLimit-Reset")
	exhausted := false
	if remaining, ok := firstHeader(resp.Header, "X-RateLimit-Remaining", "RateLimit-Remaining"); ok {
		exhausted = remaining == "0"
	}
	resetAt, resetOK := time.Time{}, false
	if resetSet {
		if secs, err := strconv.ParseInt(reset, 10, 64); err == nil {
			resetAt, resetOK = time.Unix(secs, 0), true
		}
	}
	refused := resp.StatusCode == http.StatusTooManyRequests || (resp.StatusCode == http.StatusForbidden && (exhausted || retrySet))
	var until time.Time
	switch {
	case refused && retrySet:
		until = retry
	case refused && resetOK:
		until = resetAt
	case refused:
		until = now.Add(defaultHold)
	case exhausted && resetOK:
		until = resetAt
	default:
		return 0, false
	}
	if longest := now.Add(maxHold); until.After(longest) {
		until = longest
	}
	if until.After(now) {
		c.mu.Lock()
		for key, at := range c.holds {
			if !at.After(now) {
				delete(c.holds, key)
			}
		}
		c.holds[base] = until
		c.mu.Unlock()
	}
	return max(until.Sub(now), time.Second), refused
}

func firstHeader(h http.Header, names ...string) (string, bool) {
	for _, name := range names {
		if v := strings.TrimSpace(h.Get(name)); v != "" {
			return v, true
		}
	}
	return "", false
}

// retryAfter reads Retry-After in seconds or as a date.
func retryAfter(v string, now time.Time) (time.Time, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return time.Time{}, false
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return now.Add(time.Duration(secs) * time.Second), true
	}
	if at, err := http.ParseTime(v); err == nil {
		return at, true
	}
	return time.Time{}, false
}

// RedirectError is an answer that sends the request elsewhere, which the
// door never follows. Target is where to, without what the address carries
// after its path, "" when the answer names no place.
type RedirectError struct {
	Target string
}

func (e *RedirectError) Error() string {
	if e.Target == "" {
		return "The server answers with a redirect, which is not followed: check the web address."
	}
	return fmt.Sprintf("The server redirects to %s, which is not followed: check the web address.", e.Target)
}

func redirectError(resp *http.Response) *RedirectError {
	location := resp.Header.Get("Location")
	target, err := resp.Request.URL.Parse(location)
	if location == "" || err != nil {
		return &RedirectError{}
	}
	target.User, target.RawQuery, target.ForceQuery, target.Fragment = nil, "", false, ""
	return &RedirectError{Target: quotedCut(target.String(), maxQuotedCause)}
}

// reachError words why a request found no answer.
func reachError(err error) error {
	var (
		dns       *net.DNSError
		authority x509.UnknownAuthorityError
		name      x509.HostnameError
		invalid   x509.CertificateInvalidError
		record    tls.RecordHeaderError
		request   *url.Error
		timeout   interface{ Timeout() bool }
	)
	switch {
	case errors.Is(err, errLinkLocal):
		return errors.New("The server's address is link local, which is refused.")
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &timeout) && timeout.Timeout():
		return errors.New("The server did not answer in time.")
	case errors.As(err, &authority):
		return errors.New("The server's certificate is signed by an unknown authority, an internal CA needs its CA bundle.")
	case errors.As(err, &name):
		return fmt.Errorf("The server's certificate is not valid for %s.", quoted(name.Host))
	case errors.As(err, &invalid):
		if invalid.Reason == x509.Expired {
			return errors.New("The server's certificate has expired or is not valid yet.")
		}
		return errors.New("The server's certificate is not valid.")
	case errors.As(err, &record), strings.Contains(err.Error(), "server gave HTTP response to HTTPS client"):
		return errors.New("The server does not answer with TLS here, its web address may start with http://.")
	case errors.As(err, &dns):
		return fmt.Errorf("The name %s does not resolve.", quoted(dns.Name))
	case errors.Is(err, syscall.ECONNREFUSED):
		return errors.New("The server refuses the connection.")
	}
	cause := err
	if errors.As(err, &request) {
		cause = request.Err
	}
	return fmt.Errorf("The server could not be reached: %s.", quotedCut(strings.TrimRight(cause.Error(), "."), maxQuotedCause))
}
