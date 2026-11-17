package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	checkTTL    = 5 * time.Minute
	httpTimeout = 60 * time.Second
	userAgent   = "dev-cockpit-updater"
	maxDownload = 256 << 20
)

// apiURLEnv overrides the release feed of a dev build, so the e2e suite can
// point a throwaway instance at a stub. A release build ignores it: its feed
// is pinned at build time and a stray environment must not move it.
const apiURLEnv = "DEV_COCKPIT_UPDATE_API_URL"

var (
	// ErrUpToDate reports that no pending release exists.
	ErrUpToDate = errors.New("already up to date")
	// ErrSuperseded reports that the requested version is no longer the
	// newest pending release. Callers should re-check and show the fresh
	// release notes instead of installing something the user never saw.
	ErrSuperseded = errors.New("a newer release is available")
)

type Updater struct {
	current string
	exePath string
	feed    *Feed
}

type Status struct {
	Current   string    `json:"current"`
	Latest    string    `json:"latest"`
	Available bool      `json:"available"`
	Writable  bool      `json:"writable"`
	Releases  []Release `json:"releases"`
}

type Release struct {
	Version   string `json:"version"`
	Name      string `json:"name"`
	Notes     string `json:"notes"`
	NotesHTML string `json:"notesHtml"`
	Date      string `json:"date"`
}

// New builds the updater for the running binary. feedURL is the release feed
// exactly as injected at build time, nothing here derives a URL from anything.
// format is the feed's dialect from ParseFeedFormat, and dev says whether this
// is a dev build, the only build the env override reaches, see resolveFeedURL.
func New(current, feedURL string, format FeedFormat, dev bool) (*Updater, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return &Updater{
		current: current,
		exePath: exe,
		feed:    NewFeed(resolveFeedURL(feedURL, dev), format, &http.Client{Timeout: httpTimeout}),
	}, nil
}

// resolveFeedURL applies the env override of a dev build. A release build
// takes the injected feed as it is, see apiURLEnv.
func resolveFeedURL(injected string, dev bool) string {
	if dev {
		if override := os.Getenv(apiURLEnv); override != "" {
			return override
		}
	}
	return injected
}

func (u *Updater) ExePath() string { return u.exePath }

func (u *Updater) Status(ctx context.Context, force bool) Status {
	st := Status{Current: u.current, Writable: u.writable()}
	rels, err := u.feed.Releases(ctx, force)
	if err != nil {
		log.Printf("update check: %v", err)
	}
	pending := u.pending(rels)
	st.Releases = make([]Release, 0, len(pending))
	for _, r := range pending {
		st.Releases = append(st.Releases, r.Release())
	}
	if len(pending) > 0 {
		st.Available = true
		st.Latest = pending[len(pending)-1].Version
	}
	return st
}

// Apply downloads and installs the release matching version, or the newest
// pending release when version is empty. It refreshes the release feed first,
// then installs exactly the resolved release, never silently something newer.
func (u *Updater) Apply(ctx context.Context, version string) error {
	rels, err := u.feed.Releases(ctx, true)
	if err != nil && rels == nil {
		return fmt.Errorf("check release: %w", err)
	}
	pending := u.pending(rels)
	if len(pending) == 0 {
		return ErrUpToDate
	}
	newest := pending[len(pending)-1]
	if version != "" && !SameVersion(newest.Tag, version) {
		return fmt.Errorf("version %s was requested but %s is the newest release: %w",
			strings.TrimPrefix(version, "v"), newest.Version, ErrSuperseded)
	}

	binAsset, ok := newest.Pick(platformAssetSuffix())
	if !ok {
		return fmt.Errorf("release %s has no asset for %s/%s", newest.Tag, runtime.GOOS, runtime.GOARCH)
	}
	sumAsset, ok := newest.Pick(checksumsSuffix)
	if !ok {
		return fmt.Errorf("release %s has no checksums file", newest.Tag)
	}
	sums, err := u.feed.checksumsAt(ctx, sumAsset.URL)
	if err != nil {
		return err
	}
	want, ok := sums[binAsset.Name]
	if !ok {
		return fmt.Errorf("no checksum for %s", binAsset.Name)
	}

	archive, err := u.feed.bytesAt(ctx, binAsset.URL)
	if err != nil {
		return err
	}
	if got := sha256.Sum256(archive); hex.EncodeToString(got[:]) != want {
		return ErrChecksumMismatch
	}

	bin, err := extractBinary(archive)
	if err != nil {
		return err
	}
	return u.swap(bin)
}

func (u *Updater) Restart() error {
	return syscall.Exec(u.exePath, os.Args, os.Environ())
}

func (u *Updater) pending(rels []Published) []Published {
	var out []Published
	for _, r := range rels {
		if isNewer(u.current, r.Tag) && assetsReady(r) {
			out = append(out, r)
		}
	}
	return out
}

func (u *Updater) writable() bool {
	f, err := os.CreateTemp(filepath.Dir(u.exePath), ".dc-write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

func (u *Updater) swap(bin []byte) error {
	dir := filepath.Dir(u.exePath)
	tmp, err := os.CreateTemp(dir, ".dev-cockpit-new-*")
	if err != nil {
		return fmt.Errorf("create temp binary: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp binary: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp binary: %w", err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return fmt.Errorf("chmod temp binary: %w", err)
	}
	if err := smokeTest(tmpName); err != nil {
		return fmt.Errorf("new binary failed to run: %w", err)
	}
	if err := os.Rename(tmpName, u.exePath); err != nil {
		return fmt.Errorf("replace binary: %w", err)
	}
	return nil
}

func extractBinary(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("gunzip archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || filepath.Base(hdr.Name) != "dev-cockpit" {
			continue
		}
		bin, err := io.ReadAll(io.LimitReader(tr, maxDownload))
		if err != nil {
			return nil, fmt.Errorf("read binary from archive: %w", err)
		}
		return bin, nil
	}
	return nil, errors.New("dev-cockpit binary not found in archive")
}

func smokeTest(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

const checksumsSuffix = "_checksums.txt"

// platformAssetSuffix is the tail of the release archive name for this build's
// os and arch, per the asset naming convention in AGENTS.md.
func platformAssetSuffix() string {
	return "_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
}

// assetsReady reports whether a release already carries the artifacts an update
// needs: the platform archive and the checksums file. Right after a release is
// published the build workflow has not uploaded these yet, so gating on them
// keeps a half published release from showing as available and from failing an
// apply until the assets land.
func assetsReady(r Published) bool {
	if _, ok := r.Pick(platformAssetSuffix()); !ok {
		return false
	}
	_, ok := r.Pick(checksumsSuffix)
	return ok
}

func isNewer(current, tag string) bool {
	latest := parseSemver(tag)
	if latest == nil {
		return false
	}
	cur := parseSemver(current)
	if cur == nil {
		return true
	}
	return compareSemver(latest, cur) > 0
}

func parseSemver(v string) []int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ".")
	if len(parts) > 3 {
		return nil
	}
	out := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil
		}
		out[i] = n
	}
	return out
}

func compareSemver(a, b []int) int {
	for i := 0; i < 3; i++ {
		switch {
		case a[i] > b[i]:
			return 1
		case a[i] < b[i]:
			return -1
		}
	}
	return 0
}
