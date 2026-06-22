package update

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	repo        = "marein/dev-cockpit"
	checkTTL    = 5 * time.Minute
	httpTimeout = 60 * time.Second
	userAgent   = "dev-cockpit-updater"
	maxDownload = 256 << 20
)

const defaultAPIURL = "https://api.github.com/repos/" + repo + "/releases?per_page=100"

const apiURLEnv = "DEV_COCKPIT_UPDATE_API_URL"

type Updater struct {
	current string
	exePath string
	apiURL  string
	client  *http.Client

	mu        sync.Mutex
	etag      string
	cached    []ghRelease
	checkedAt time.Time
}

type Status struct {
	Current   string    `json:"current"`
	Latest    string    `json:"latest"`
	Available bool      `json:"available"`
	Writable  bool      `json:"writable"`
	Releases  []Release `json:"releases"`
}

type Release struct {
	Version string `json:"version"`
	Name    string `json:"name"`
	Notes   string `json:"notes"`
	Date    string `json:"date"`
}

type ghRelease struct {
	TagName     string  `json:"tag_name"`
	Name        string  `json:"name"`
	Body        string  `json:"body"`
	PublishedAt string  `json:"published_at"`
	Draft       bool    `json:"draft"`
	Prerelease  bool    `json:"prerelease"`
	Assets      []asset `json:"assets"`
}

type asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

func New(current string) (*Updater, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	apiURL := defaultAPIURL
	if override := os.Getenv(apiURLEnv); override != "" {
		apiURL = override
	}
	return &Updater{
		current: current,
		exePath: exe,
		apiURL:  apiURL,
		client:  &http.Client{Timeout: httpTimeout},
	}, nil
}

func (u *Updater) ExePath() string { return u.exePath }

func (u *Updater) Status(ctx context.Context, force bool) Status {
	st := Status{Current: u.current, Writable: u.writable()}
	rels, err := u.releases(ctx, force)
	if err != nil {
		log.Printf("update check: %v", err)
	}
	pending := u.pending(rels)
	st.Releases = make([]Release, 0, len(pending))
	for _, r := range pending {
		st.Releases = append(st.Releases, Release{
			Version: strings.TrimPrefix(r.TagName, "v"),
			Name:    r.Name,
			Notes:   r.Body,
			Date:    r.PublishedAt,
		})
	}
	if len(pending) > 0 {
		st.Available = true
		st.Latest = strings.TrimPrefix(pending[len(pending)-1].TagName, "v")
	}
	return st
}

func (u *Updater) Apply(ctx context.Context) error {
	rels, err := u.releases(ctx, false)
	if err != nil && rels == nil {
		return fmt.Errorf("check release: %w", err)
	}
	pending := u.pending(rels)
	if len(pending) == 0 {
		return errors.New("already up to date")
	}
	newest := pending[len(pending)-1]

	target := "_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	binAsset, ok := findAsset(newest.Assets, target)
	if !ok {
		return fmt.Errorf("release %s has no asset for %s/%s", newest.TagName, runtime.GOOS, runtime.GOARCH)
	}
	sumAsset, ok := findAsset(newest.Assets, "_checksums.txt")
	if !ok {
		return fmt.Errorf("release %s has no checksums file", newest.TagName)
	}

	sums, err := u.downloadChecksums(ctx, sumAsset.URL)
	if err != nil {
		return err
	}
	want, ok := sums[binAsset.Name]
	if !ok {
		return fmt.Errorf("no checksum for %s", binAsset.Name)
	}

	archive, err := u.download(ctx, binAsset.URL)
	if err != nil {
		return err
	}
	if got := sha256.Sum256(archive); hex.EncodeToString(got[:]) != want {
		return errors.New("checksum mismatch: download corrupt or tampered")
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

func (u *Updater) pending(rels []ghRelease) []ghRelease {
	var out []ghRelease
	for _, r := range rels {
		if r.Draft || r.Prerelease || parseSemver(r.TagName) == nil {
			continue
		}
		if isNewer(u.current, r.TagName) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return compareSemver(parseSemver(out[i].TagName), parseSemver(out[j].TagName)) < 0
	})
	return out
}

func (u *Updater) releases(ctx context.Context, force bool) ([]ghRelease, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !force && !u.checkedAt.IsZero() && time.Since(u.checkedAt) < checkTTL {
		return u.cached, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.apiURL, nil)
	if err != nil {
		return u.cached, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", userAgent)
	if u.etag != "" {
		req.Header.Set("If-None-Match", u.etag)
	}

	resp, err := u.client.Do(req)
	if err != nil {
		return u.cached, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotModified:
		u.checkedAt = time.Now()
		return u.cached, nil
	case http.StatusOK:
		var rels []ghRelease
		if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&rels); err != nil {
			return u.cached, fmt.Errorf("decode releases: %w", err)
		}
		u.cached = rels
		u.etag = resp.Header.Get("ETag")
		u.checkedAt = time.Now()
		return u.cached, nil
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return u.cached, fmt.Errorf("github releases: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
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

func (u *Updater) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxDownload))
}

func (u *Updater) downloadChecksums(ctx context.Context, url string) (map[string]string, error) {
	raw, err := u.download(ctx, url)
	if err != nil {
		return nil, err
	}
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

func findAsset(assets []asset, suffix string) (asset, bool) {
	for _, a := range assets {
		if strings.HasSuffix(a.Name, suffix) {
			return a, true
		}
	}
	return asset{}, false
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
