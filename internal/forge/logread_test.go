package forge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// A log below the API goes with the token and keeps its last bytes, all of
// it when it is short; a web page is no log, and a log longer than the door
// reads is refused rather than shown from its middle.
func TestTailKeepsTheEndOfALog(t *testing.T) {
	var auth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization") + " " + r.Header.Get("Accept"))
		switch r.URL.Path {
		case "/short":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "short log\n")
		case "/long":
			w.Header().Set("Content-Type", "text/plain")
			for i := range 100_000 {
				fmt.Fprintf(w, "line %06d\n", i)
			}
		case "/page":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, "<html>sign in</html>")
		case "/huge":
			w.Header().Set("Content-Type", "application/octet-stream")
			chunk := strings.Repeat("x", 1<<20)
			for range maxLog>>20 + 1 {
				if _, err := io.WriteString(w, chunk); err != nil {
					return
				}
			}
		}
	}))
	defer srv.Close()
	a := NewClient().API(srv.URL, Host{}, secret, nil)
	raw, cut, err := a.Tail(context.Background(), "/short", 64)
	if err != nil || string(raw) != "short log\n" || cut {
		t.Fatalf("a short log: %q, %v, %v", raw, cut, err)
	}
	if got := auth.Load(); got != "Bearer "+secret+" text/plain" {
		t.Fatalf("asked with %q", got)
	}
	raw, cut, err = a.Tail(context.Background(), "/long", 21)
	if err != nil || string(raw) != "e 099998\nline 099999\n" || !cut {
		t.Fatalf("a long log: %q, %v, %v", raw, cut, err)
	}
	if _, _, err := a.Tail(context.Background(), "/page", 64); err == nil || err.Error() != "The server's answer is a web page, no log." {
		t.Fatalf("a web page: %v", err)
	}
	if _, _, err := a.Tail(context.Background(), "/huge", 64); err == nil || err.Error() != "The log is larger than 32 MiB, its end is not read." {
		t.Fatalf("a huge log: %v", err)
	}
}

// A download address comes as a temporary redirect, which is answered and
// not followed; a permanent one moved what was asked for and is the
// redirect error, and an answer without a redirect is none.
func TestRedirectAnswersTheDownloadAddressWithoutFollowingIt(t *testing.T) {
	var reached atomic.Bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true) }))
	defer elsewhere.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/found":
			http.Redirect(w, r, elsewhere.URL+"/blob/log.txt?sig=abc", http.StatusFound)
		case "/relative":
			w.Header().Set("Location", "/blob/here")
			w.WriteHeader(http.StatusTemporaryRedirect)
		case "/moved":
			http.Redirect(w, r, elsewhere.URL+"/repositories/5/logs?sig=abc", http.StatusMovedPermanently)
		case "/direct":
			writeJSON(w, `{}`)
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	a := NewClient().API(srv.URL, Host{}, secret, nil)
	target, err := a.Redirect(context.Background(), "/found")
	if err != nil || target.String() != elsewhere.URL+"/blob/log.txt?sig=abc" {
		t.Fatalf("a download address: %v, %v", target, err)
	}
	if target, err = a.Redirect(context.Background(), "/relative"); err != nil || target.String() != srv.URL+"/blob/here" {
		t.Fatalf("a relative download address: %v, %v", target, err)
	}
	_, err = a.Redirect(context.Background(), "/moved")
	var redirect *RedirectError
	if !errors.As(err, &redirect) || redirect.Target != elsewhere.URL+"/repositories/5/logs" {
		t.Fatalf("a permanent redirect: %v", err)
	}
	if _, err := a.Redirect(context.Background(), "/direct"); err == nil || err.Error() != "The server answered without a download address." {
		t.Fatalf("an answer without a redirect: %v", err)
	}
	var status *StatusError
	if _, err := a.Redirect(context.Background(), "/missing"); !errors.As(err, &status) || status.Code != http.StatusNotFound {
		t.Fatalf("a missing log: %v", err)
	}
	if reached.Load() {
		t.Fatal("a redirect was followed")
	}
}

// The download goes without the token and the provider's headers, follows
// no redirect, and an http address is read only where the API itself is
// plain http.
func TestTailAtDownloadsWithoutTheToken(t *testing.T) {
	var seen atomic.Value
	var reached atomic.Bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true) }))
	defer elsewhere.Close()
	blob := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Store(r.Header.Clone())
		switch r.URL.Path {
		case "/log":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "\xef\xbb\xbfjob log\n")
		case "/again":
			http.Redirect(w, r, elsewhere.URL+"/log", http.StatusFound)
		case "/expired":
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer blob.Close()
	a := NewClient().API(blob.URL+"/api/v3", Host{}, secret, http.Header{"Accept": {"application/vnd.github+json"}, "X-Github-Api-Version": {"2022-11-28"}})
	at := func(path string) *url.URL {
		u, _ := url.Parse(blob.URL + path + "?sig=abc")
		return u
	}
	raw, cut, err := a.TailAt(context.Background(), at("/log"), 1024)
	if err != nil || string(raw) != "\xef\xbb\xbfjob log\n" || cut {
		t.Fatalf("a download: %q, %v, %v", raw, cut, err)
	}
	header := seen.Load().(http.Header)
	if header.Get("Authorization") != "" || header.Get("X-Github-Api-Version") != "" || strings.Contains(header.Get("Accept"), "github") || header.Get("User-Agent") != "dev-cockpit" {
		t.Fatalf("the download carried %v", header)
	}
	var redirect *RedirectError
	if _, _, err := a.TailAt(context.Background(), at("/again"), 1024); !errors.As(err, &redirect) || reached.Load() {
		t.Fatalf("a download that redirects again: %v, followed %v", err, reached.Load())
	}
	var status *StatusError
	if _, _, err := a.TailAt(context.Background(), at("/expired"), 1024); !errors.As(err, &status) || status.Code != http.StatusForbidden {
		t.Fatalf("an expired download: %v", err)
	}
	secure := NewClient().API("https://github.example.com/api/v3", Host{}, secret, nil)
	if _, _, err := secure.TailAt(context.Background(), at("/log"), 1024); err == nil || err.Error() != "The server's download address is not https." {
		t.Fatalf("http below an https API: %v", err)
	}
	for _, target := range []string{"file:///etc/passwd", "ftp://example.com/log"} {
		u, _ := url.Parse(target)
		if _, _, err := a.TailAt(context.Background(), u, 1024); err == nil || err.Error() != "The server's download address is not https." {
			t.Fatalf("%s: %v", target, err)
		}
	}
	link, _ := url.Parse("http://169.254.169.254/latest")
	if _, _, err := a.TailAt(context.Background(), link, 1024); err == nil || err.Error() != "The server's address is link local, which is refused." {
		t.Fatalf("a link local download: %v", err)
	}
}
