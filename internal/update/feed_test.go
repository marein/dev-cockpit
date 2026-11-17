package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
)

// stubFeed serves a GitHub releases feed with an ETag, and the release files.
type stubFeed struct {
	*httptest.Server
	releases []map[string]any
	files    map[string][]byte
	asked    atomic.Int32
	notMod   atomic.Int32
}

func newStubFeed(t *testing.T) *stubFeed {
	t.Helper()
	f := &stubFeed{files: map[string][]byte{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/releases" {
			f.asked.Add(1)
			raw, _ := json.Marshal(f.releases)
			etag := fmt.Sprintf(`"%x"`, sha256.Sum256(raw))
			if r.Header.Get("If-None-Match") == etag {
				f.notMod.Add(1)
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", etag)
			_, _ = w.Write(raw)
			return
		}
		body, ok := f.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(f.Close)
	return f
}

// release adds a release of files plus a checksums file listing them.
func (f *stubFeed) release(tag string, prerelease bool, files map[string][]byte) {
	var sums bytes.Buffer
	var assets []map[string]string
	for name, body := range files {
		f.files["/"+tag+"/"+name] = body
		sum := sha256.Sum256(body)
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), name)
		assets = append(assets, map[string]string{"name": name, "browser_download_url": f.URL + "/" + tag + "/" + name})
	}
	sumsName := "x_" + tag + "_checksums.txt"
	f.files["/"+tag+"/"+sumsName] = sums.Bytes()
	assets = append(assets, map[string]string{"name": sumsName, "browser_download_url": f.URL + "/" + tag + "/" + sumsName})
	f.releases = append(f.releases, map[string]any{"tag_name": tag, "name": tag, "body": "Notes of " + tag, "prerelease": prerelease, "assets": assets})
}

func (f *stubFeed) feed() *Feed {
	return NewFeed(f.URL+"/releases", githubFormat, &http.Client{})
}

// Releases sorts oldest first, drops prereleases and tags that are no semver,
// caches for the TTL and asks again with the ETag.
func TestFeedReleases(t *testing.T) {
	stub := newStubFeed(t)
	stub.release("v1.10.0", false, nil)
	stub.release("v1.2.0", false, nil)
	stub.release("v2.0.0", true, nil)
	stub.release("nightly", false, nil)
	feed := stub.feed()
	rels, err := feed.Releases(context.Background(), true)
	if err != nil {
		t.Fatalf("Releases() = %v", err)
	}
	var versions []string
	for _, r := range rels {
		versions = append(versions, r.Version)
	}
	if fmt.Sprint(versions) != "[1.2.0 1.10.0]" {
		t.Fatalf("Releases() = %v, want [1.2.0 1.10.0]", versions)
	}
	if _, err := feed.Releases(context.Background(), false); err != nil || stub.asked.Load() != 1 {
		t.Fatalf("a second read within the TTL asked the feed again: %d asks, %v", stub.asked.Load(), err)
	}
	again, err := feed.Releases(context.Background(), true)
	if err != nil || len(again) != 2 || stub.notMod.Load() != 1 {
		t.Fatalf("a forced read was not asked on the ETag: %d not modified, %d releases, %v", stub.notMod.Load(), len(again), err)
	}
	if rels[1].Release().NotesHTML != "<p>Notes of v1.10.0</p>\n" {
		t.Fatalf("notes rendered %q", rels[1].Release().NotesHTML)
	}
}

// An error status comes back as a StatusError with its code.
func TestFeedStatusError(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	_, err := NewFeed(server.URL, githubFormat, &http.Client{}).Releases(context.Background(), true)
	var status *StatusError
	if !errors.As(err, &status) || status.Code != http.StatusNotFound {
		t.Fatalf("Releases() = %v, want a StatusError 404", err)
	}
}

// Fetch refuses a download that does not match its checksum, is not listed
// or exceeds the limit.
func TestFeedFetch(t *testing.T) {
	stub := newStubFeed(t)
	stub.release("v1.0.0", false, map[string][]byte{"x_1.0.0_any.tar.gz": []byte("the archive")})
	feed := stub.feed()
	rels, _ := feed.Releases(context.Background(), true)
	rel := rels[0]
	archive, _ := rel.Pick("_linux_arm64.tar.gz", "_any.tar.gz")
	sums, _ := rel.Pick("_checksums.txt")
	var got bytes.Buffer
	if err := feed.Fetch(context.Background(), sums, archive, &got, 1<<20); err != nil || got.String() != "the archive" {
		t.Fatalf("Fetch() = %v, %q", err, got.String())
	}
	if err := feed.Fetch(context.Background(), sums, archive, &bytes.Buffer{}, 4); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Fetch() beyond the bound = %v, want ErrTooLarge", err)
	}
	stub.files["/v1.0.0/x_1.0.0_any.tar.gz"] = []byte("tampered")
	if err := feed.Fetch(context.Background(), sums, archive, &bytes.Buffer{}, 1<<20); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("Fetch() of a changed file = %v, want ErrChecksumMismatch", err)
	}
	unlisted := Asset{Name: "other.tar.gz", URL: archive.URL}
	if err := feed.Fetch(context.Background(), sums, unlisted, &bytes.Buffer{}, 1<<20); !errors.Is(err, ErrNoChecksum) || err.Error() != "no checksum for other.tar.gz" {
		t.Fatalf("Fetch() of an unlisted file = %v, want no checksum for other.tar.gz", err)
	}
}

// The cockpit's updater works over the shared Feed: Status lists the pending
// release, Apply verifies, smoke tests and swaps in its binary.
func TestUpdaterApplyThroughTheFeed(t *testing.T) {
	stub := newStubFeed(t)
	binary := []byte("#!/bin/sh\necho dev-cockpit 9.9.9\n")
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "dev-cockpit", Mode: 0o755, Size: int64(len(binary)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(binary)
	_ = tw.Close()
	_ = gz.Close()
	name := "dev-cockpit_v9.9.9_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	stub.release("v9.9.9", false, map[string][]byte{name: archive.Bytes()})
	stub.release("v1.0.0", false, nil)
	exe := filepath.Join(t.TempDir(), "dev-cockpit")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\necho old\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	u := &Updater{current: "1.0.0", exePath: exe, feed: stub.feed()}
	st := u.Status(context.Background(), true)
	if !st.Available || st.Latest != "9.9.9" || len(st.Releases) != 1 || st.Releases[0].Notes != "Notes of v9.9.9" {
		t.Fatalf("Status() = %+v", st)
	}
	if err := u.Apply(context.Background(), "1.5.0"); !errors.Is(err, ErrSuperseded) {
		t.Fatalf("Apply() of a version not the newest = %v, want ErrSuperseded", err)
	}
	if err := u.Apply(context.Background(), "9.9.9"); err != nil {
		t.Fatalf("Apply() = %v", err)
	}
	got, _ := os.ReadFile(exe)
	if !bytes.Equal(got, binary) {
		t.Fatalf("the binary is %q after Apply, want the release's", got)
	}
	u.current = "9.9.9"
	if err := u.Apply(context.Background(), ""); !errors.Is(err, ErrUpToDate) {
		t.Fatalf("Apply() when up to date = %v, want ErrUpToDate", err)
	}
}

// Apply reports up to date once the feed answered without an installable
// release, also when it fails after, as before the Feed was shared. Only a
// feed that never answered fails the check.
func TestUpdaterApplyAfterAnAnswerWithoutRelease(t *testing.T) {
	for name, tc := range map[string]struct {
		format FeedFormat
		body   string
	}{
		"github empty":      {githubFormat, `[]`},
		"github prerelease": {githubFormat, `[{"tag_name":"v2.0.0","prerelease":true}]`},
		"gitlab empty":      {gitlabFormat, `[]`},
	} {
		t.Run(name, func(t *testing.T) {
			var down atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if down.Load() {
					http.Error(w, "stub down", http.StatusInternalServerError)
					return
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(server.Close)
			exe := filepath.Join(t.TempDir(), "dev-cockpit")
			u := &Updater{current: "1.0.0", exePath: exe, feed: NewFeed(server.URL, tc.format, &http.Client{})}
			if err := u.Apply(context.Background(), ""); !errors.Is(err, ErrUpToDate) {
				t.Fatalf("Apply() on the answer = %v, want ErrUpToDate", err)
			}
			down.Store(true)
			if err := u.Apply(context.Background(), ""); !errors.Is(err, ErrUpToDate) {
				t.Fatalf("Apply() once the feed fails = %v, want ErrUpToDate", err)
			}
			never := &Updater{current: "1.0.0", exePath: exe, feed: NewFeed(server.URL, tc.format, &http.Client{})}
			if err := never.Apply(context.Background(), ""); err == nil || errors.Is(err, ErrUpToDate) || err.Error() != "check release: "+tc.format.name+" releases: 500 Internal Server Error: stub down" {
				t.Fatalf("Apply() on a feed that never answered = %v", err)
			}
		})
	}
}
