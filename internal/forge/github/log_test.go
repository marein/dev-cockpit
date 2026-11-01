package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
)

// fakeLogs is an Enterprise Server that answers an Actions job's logs with
// a redirect to a download address on another server, the way GitHub hands
// out blob storage, or with what answer names. The download keeps the
// headers it was asked with and refuses below /expired, as an address past
// its minute does.
type fakeLogs struct {
	answer   int
	download string

	mu   sync.Mutex
	seen []http.Header
}

func (f *fakeLogs) api(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+token {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"Bad credentials","documentation_url":"https://docs.github.com/rest","status":"401"}`)
		return
	}
	if r.URL.EscapedPath() != repoAt+"/actions/jobs/150/logs" {
		http.NotFound(w, r)
		return
	}
	switch f.answer {
	case http.StatusFound:
		w.Header().Set("Location", f.download+"/actions-results/150.txt?sv=2025&sig=s3cret")
		w.WriteHeader(http.StatusFound)
	case http.StatusMovedPermanently:
		http.Redirect(w, r, "/api/v3/repositories/42/actions/jobs/150/logs", http.StatusMovedPermanently)
	default:
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(f.answer)
		fmt.Fprintf(w, `{"message":%q,"status":"%d"}`, http.StatusText(f.answer), f.answer)
	}
}

func (f *fakeLogs) blob(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.seen = append(f.seen, r.Header.Clone())
	f.mu.Unlock()
	if r.URL.Query().Get("sig") != "s3cret" || strings.HasPrefix(r.URL.Path, "/expired/") {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprint(w, "\xef\xbb\xbf2026-10-28T09:00:00.0000000Z ##[error]Process completed with exit code 1.\n")
}

func readJobLog(t *testing.T, webURL string, job forge.Job) ([]byte, bool, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	raw, cut, err := Provider{}.JobLog(ctx, forge.NewClient(), forge.Binding{Host: host(webURL), Path: "octo/app"}, token, job, 1024)
	if err != nil && (strings.Contains(err.Error(), token) || strings.Contains(err.Error(), "s3cret")) {
		t.Fatalf("the error names a secret: %v", err)
	}
	return raw, cut, err
}

// The log of an Actions job is read at the address GitHub redirects to, as a
// request of its own that carries neither the token nor GitHub's headers.
func TestAnActionsJobLogIsDownloadedWithoutTheToken(t *testing.T) {
	f := &fakeLogs{answer: http.StatusFound}
	blob := httptest.NewServer(http.HandlerFunc(f.blob))
	defer blob.Close()
	f.download = blob.URL
	srv := httptest.NewServer(http.HandlerFunc(f.api))
	defer srv.Close()
	raw, cut, err := readJobLog(t, srv.URL, forge.Job{ID: "150", Name: "e2e"})
	if err != nil || cut || string(raw) != "\xef\xbb\xbf2026-10-28T09:00:00.0000000Z ##[error]Process completed with exit code 1.\n" {
		t.Fatalf("got %q, %v, %v", raw, cut, err)
	}
	if len(f.seen) != 1 || f.seen[0].Get("Authorization") != "" || f.seen[0].Get("X-Github-Api-Version") != "" || strings.Contains(f.seen[0].Get("Accept"), "github") {
		t.Fatalf("the download was asked with %v", f.seen)
	}
}

// What GitHub and the download answer instead of a log is said with the
// job's name, the download's address never.
func TestAnActionsJobLogGitHubRefusesIsSaid(t *testing.T) {
	f := &fakeLogs{}
	blob := httptest.NewServer(http.HandlerFunc(f.blob))
	defer blob.Close()
	srv := httptest.NewServer(http.HandlerFunc(f.api))
	defer srv.Close()
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	address := strings.TrimPrefix(srv.URL, "http://") + "/octo/app"
	for _, c := range []struct {
		name     string
		answer   int
		download string
		says     string
	}{
		{"not finished or gone", http.StatusNotFound, "", `GitHub has no log of "e2e": the job has not finished, or its log has expired.`},
		{"expired", http.StatusGone, "", `GitHub has no log of "e2e": the job has not finished, or its log has expired.`},
		{"forbidden", http.StatusForbidden, "", "The token may not read the Actions logs of " + address + "."},
		{"moved", http.StatusMovedPermanently, "", "The forge redirects the read of " + address + " to " + srv.URL + "/api/v3/repositories/42/actions/jobs/150/logs, which is not followed: if the repository was renamed or moved, give the origin its new path or set it under the project's Forge, else check the host's web address."},
		{"download expired", http.StatusFound, blob.URL + "/expired", `The address GitHub hands out to download the log of "e2e" answered 403 Forbidden, try again.`},
		{"download unreachable", http.StatusFound, closed.URL, `The log of "e2e" could not be downloaded. The server refuses the connection.`},
		{"download not https", http.StatusFound, "ftp://files.example.com", `The log of "e2e" could not be downloaded. The server's download address is not https.`},
	} {
		f.answer, f.download = c.answer, c.download
		if _, _, err := readJobLog(t, srv.URL, forge.Job{ID: "150", Name: "e2e"}); err == nil || err.Error() != c.says {
			t.Errorf("%s: %v, want %q", c.name, err, c.says)
		}
	}
}
