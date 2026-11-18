package update

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	// ErrChecksumMismatch reports a download that does not match its checksum.
	ErrChecksumMismatch = errors.New("checksum mismatch: download corrupt or tampered")
	// ErrNoChecksum reports a checksums file that does not list the download.
	ErrNoChecksum = errors.New("no checksum")
	// ErrTooLarge reports a download beyond the caller's limit.
	ErrTooLarge = errors.New("download too large")
)

// StatusError is a feed answer other than 200 or 304.
type StatusError struct {
	Code int
	text string
}

func (e *StatusError) Error() string { return e.text }

// Feed reads a release feed in one FeedFormat dialect, for the cockpit's
// updater and the external plugins. It keeps the last answer for checkTTL and
// asks again with its ETag, which spares the forge's rate limit.
type Feed struct {
	url    string
	format FeedFormat
	client *http.Client

	mu        sync.Mutex
	etag      string
	cached    []release
	checkedAt time.Time
}

// NewFeed returns the feed at url in format, fetched through client.
func NewFeed(url string, format FeedFormat, client *http.Client) *Feed {
	return &Feed{url: url, format: format, client: client}
}

// Published is an installable release: semver tag, no prerelease, draft or
// upcoming release.
type Published struct {
	Version string
	Tag     string
	Name    string
	Notes   string
	Date    string
	Assets  []Asset
}

// Asset is one downloadable file of a release.
type Asset struct {
	Name string
	URL  string
}

// Releases answers the installable releases, oldest first. An error comes
// with those of the last answer that went through, nil only when none did,
// so Apply tells an unreachable feed from one with nothing to install.
func (f *Feed) Releases(ctx context.Context, force bool) ([]Published, error) {
	rels, err := f.fetch(ctx, force)
	if rels == nil {
		return nil, err
	}
	out := make([]Published, 0, len(rels))
	for _, r := range rels {
		if r.Prerelease || parseSemver(r.Tag) == nil {
			continue
		}
		p := Published{Version: strings.TrimPrefix(r.Tag, "v"), Tag: r.Tag, Name: r.Name, Notes: r.Notes, Date: r.Date}
		for _, a := range r.Assets {
			p.Assets = append(p.Assets, Asset{Name: a.Name, URL: a.URL})
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return compareSemver(parseSemver(out[i].Tag), parseSemver(out[j].Tag)) < 0
	})
	return out, err
}

func (f *Feed) fetch(ctx context.Context, force bool) ([]release, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !force && !f.checkedAt.IsZero() && time.Since(f.checkedAt) < checkTTL {
		return f.cached, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return f.cached, err
	}
	req.Header.Set("User-Agent", userAgent)
	f.format.prepare(req)
	// The conditional request is plain HTTP, not one dialect's: a feed that
	// answered with an ETag is asked with If-None-Match the next round, one
	// that never sends one is asked plainly.
	if f.etag != "" {
		req.Header.Set("If-None-Match", f.etag)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return f.cached, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotModified:
		f.checkedAt = time.Now()
		return f.cached, nil
	case http.StatusOK:
		rels, err := f.format.decode(io.LimitReader(resp.Body, 8<<20))
		if err != nil {
			return f.cached, fmt.Errorf("decode releases: %w", err)
		}
		f.cached = rels
		f.etag = resp.Header.Get("ETag")
		f.checkedAt = time.Now()
		return f.cached, nil
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return f.cached, &StatusError{Code: resp.StatusCode, text: fmt.Sprintf("%s releases: %s: %s", f.format.name, resp.Status, strings.TrimSpace(string(body)))}
	}
}

// Pick answers an asset whose name ends in a suffix, trying suffixes in order.
func (p Published) Pick(suffixes ...string) (Asset, bool) {
	for _, suffix := range suffixes {
		for _, a := range p.Assets {
			if strings.HasSuffix(a.Name, suffix) {
				return a, true
			}
		}
	}
	return Asset{}, false
}

// Release is p for display, its notes rendered to HTML.
func (p Published) Release() Release {
	return Release{Version: p.Version, Name: p.Name, Notes: p.Notes, NotesHTML: renderNotes(p.Notes), Date: p.Date}
}

// Fetch downloads archive into w, at most limit bytes, and verifies it
// against the checksums file sums. w gets the bytes before the check, trust
// them only when Fetch answers nil.
func (f *Feed) Fetch(ctx context.Context, sums, archive Asset, w io.Writer, limit int64) error {
	list, err := f.checksumsAt(ctx, sums.URL)
	if err != nil {
		return err
	}
	want, ok := list[archive.Name]
	if !ok {
		return fmt.Errorf("%w for %s", ErrNoChecksum, archive.Name)
	}
	hash := sha256.New()
	n, err := f.copyFrom(ctx, archive.URL, io.MultiWriter(w, hash), limit+1)
	if err != nil {
		return err
	}
	if n > limit {
		return fmt.Errorf("%w: %s is larger than %d bytes", ErrTooLarge, archive.Name, limit)
	}
	if hex.EncodeToString(hash.Sum(nil)) != want {
		return ErrChecksumMismatch
	}
	return nil
}

// bytesAt downloads at most maxDownload bytes of url.
func (f *Feed) bytesAt(ctx context.Context, url string) ([]byte, error) {
	var b bytes.Buffer
	_, err := f.copyFrom(ctx, url, &b, maxDownload)
	return b.Bytes(), err
}

func (f *Feed) checksumsAt(ctx context.Context, url string) (map[string]string, error) {
	raw, err := f.bytesAt(ctx, url)
	if err != nil {
		return nil, err
	}
	return checksums(raw)
}

// copyFrom copies at most limit bytes of url into w and stops there without
// an error.
func (f *Feed) copyFrom(ctx context.Context, url string, w io.Writer, limit int64) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := f.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return io.Copy(w, io.LimitReader(resp.Body, limit))
}

// checksums parses `<sha256>  <name>` lines, a binary mode `*` before the
// name allowed.
func checksums(raw []byte) (map[string]string, error) {
	sums := make(map[string]string)
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 {
			continue
		}
		sums[strings.TrimPrefix(fields[1], "*")] = fields[0]
	}
	return sums, sc.Err()
}

// Newer answers whether version is a semver newer than current. A current
// that is no semver, a dev build or none, is older than any.
func Newer(current, version string) bool {
	return isNewer(current, version)
}

// SameVersion answers whether a and b are the same semver, a leading v
// ignored.
func SameVersion(a, b string) bool {
	av, bv := parseSemver(a), parseSemver(b)
	return av != nil && bv != nil && compareSemver(av, bv) == 0
}
