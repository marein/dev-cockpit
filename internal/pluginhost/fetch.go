package pluginhost

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/update"
)

// Release assets are named like the cockpit's own with the plugin id in
// front: an archive per os and arch or one _any archive for a plugin without
// a binary, and a checksums file.
const (
	anySuffix       = "_any.tar.gz"
	checksumsSuffix = "_checksums.txt"
)

func platformSuffix() string { return "_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz" }

// fits answers whether a release has an archive for this platform or any,
// and a checksums file. A release whose files are still uploading is not
// offered yet.
func fits(rel update.Published) bool {
	if _, ok := rel.Pick(platformSuffix(), anySuffix); !ok {
		return false
	}
	_, ok := rel.Pick(checksumsSuffix)
	return ok
}

// pendingFor answers the releases newer than version that fit, oldest first.
func pendingFor(rels []update.Published, version string) []update.Published {
	var out []update.Published
	for _, rel := range rels {
		if update.Newer(version, rel.Version) && fits(rel) {
			out = append(out, rel)
		}
	}
	return out
}

// staged is a verified release unpacked into a directory of the plugins
// folder and checked like a load.
type staged struct {
	dir      string
	root     string
	manifest *Manifest
	release  update.Published
	archive  string
}

func (s *staged) discard() {
	if s != nil && s.dir != "" {
		_ = os.RemoveAll(s.dir)
	}
}

// stage fetches rel into a new directory of the plugins folder. An error
// leaves nothing behind.
func (h *Host) stage(ctx context.Context, feed *update.Feed, rel update.Published) (*staged, error) {
	archive, _ := rel.Pick(platformSuffix(), anySuffix)
	sums, _ := rel.Pick(checksumsSuffix)
	if err := os.MkdirAll(h.Dir(), 0o700); err != nil {
		return nil, err
	}
	s := &staged{dir: filepath.Join(h.Dir(), ".fetch-"+randomHex(6)), release: rel, archive: archive.Name}
	if err := os.Mkdir(s.dir, 0o700); err != nil {
		return nil, err
	}
	err := h.fill(ctx, s, feed, sums, archive)
	if err != nil {
		s.discard()
		return nil, err
	}
	return s, nil
}

func (h *Host) fill(ctx context.Context, s *staged, feed *update.Feed, sums, archive update.Asset) error {
	file, err := os.CreateTemp(s.dir, "archive-*")
	if err != nil {
		return err
	}
	defer file.Close()
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	if err := feed.Fetch(ctx, sums, archive, file, maxInstallBytes); err != nil {
		return fetchRefusal(s.release.Version, archive.Name, err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	unpacked := filepath.Join(s.dir, "plugin")
	if err := os.Mkdir(unpacked, 0o700); err != nil {
		return err
	}
	root, err := unpack(file, unpacked)
	if err != nil {
		return err
	}
	_ = os.Remove(file.Name())
	m, err := verifyDir(root)
	if err != nil {
		return refuse("Release %s refused. %s", s.release.Version, err.Error())
	}
	if !update.SameVersion(m.Version, s.release.Version) {
		return refuse("Release %s refused: its plugin.json says version %s.", s.release.Version, cut(m.Version))
	}
	if h.compiled[m.ID] {
		return refuse("Release %s refused: a built in plugin has the id %s.", s.release.Version, m.ID)
	}
	s.root, s.manifest = root, m
	return nil
}

// fetchRefusal words a failed download as a refusal.
func fetchRefusal(version, archive string, err error) error {
	switch {
	case errors.Is(err, update.ErrChecksumMismatch):
		return refuse("Release %s refused: %s does not match its checksum.", version, cut(archive))
	case errors.Is(err, update.ErrNoChecksum):
		return refuse("Release %s refused: its checksums file does not list %s.", version, cut(archive))
	case errors.Is(err, update.ErrTooLarge):
		return refuse("Release %s refused: a file is larger than 256 MiB.", version)
	case errors.Is(err, context.DeadlineExceeded):
		return refuse("The download of release %s took longer than 5 minutes.", version)
	default:
		log.Printf("plugins: the download of %s failed: %v", archive, err)
		return refuse("Release %s could not be downloaded.", version)
	}
}

// feedRefusal words a failed feed read as a refusal.
func feedRefusal(repo Repository, err error) error {
	var status *update.StatusError
	switch {
	case errors.As(err, &status) && status.Code == 404:
		return refuse("No public repository at %s.", repo.Short())
	case errors.As(err, &status) && (status.Code == 403 || status.Code == 429):
		return refuse("%s rate limits the requests, try again later.", repo.Host)
	case errors.As(err, &status):
		return refuse("%s answered %d for the releases of %s.", repo.Host, status.Code, repo.Short())
	default:
		log.Printf("plugins: the releases of %s could not be read: %v", repo.URL, err)
		return refuse("The releases of %s could not be read.", repo.Short())
	}
}

func forgeName(forge string) string {
	if forge == ForgeGitHub {
		return "GitHub"
	}
	return "GitLab"
}

// unpack extracts a gzipped tar into dir through an os.Root, so no path or
// link escapes dir. It takes directories, regular files and relative links
// that stay inside, within the bounds of Install, and answers the directory
// holding plugin.json.
func unpack(r io.Reader, dir string) (string, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return "", refuse("The archive is not a gzipped tar.")
	}
	defer gz.Close()
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	tr := tar.NewReader(gz)
	entries, size := 0, int64(0)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", refuse("The archive is not a gzipped tar or is truncated.")
		}
		if hdr.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name := path.Clean(strings.TrimPrefix(hdr.Name, "./"))
		if name == "." {
			continue
		}
		if !filepath.IsLocal(name) || len(name) > maxInstallPath || hasControl(name) {
			return "", refuse("The archive path %s leaves the plugin directory.", cut(hdr.Name))
		}
		if entries++; entries > maxInstallFiles {
			return "", refuse("The archive has more than %d entries.", maxInstallFiles)
		}
		if dir := path.Dir(name); dir != "." {
			if err := root.MkdirAll(dir, 0o700); err != nil {
				return "", refuse("The archive path %s leaves the plugin directory.", cut(hdr.Name))
			}
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, fs.FileMode(hdr.Mode).Perm()|0o700); err != nil {
				return "", refuse("The archive has the directory %s twice or behind a link.", cut(name))
			}
		case tar.TypeReg:
			if size += hdr.Size; size > maxInstallBytes {
				return "", refuse("The archive unpacks to more than 256 MiB.")
			}
			if err := writeEntry(root, name, fs.FileMode(hdr.Mode).Perm()|0o600, tr); err != nil {
				return "", err
			}
		case tar.TypeSymlink:
			target := hdr.Linkname
			if filepath.IsAbs(target) || !filepath.IsLocal(filepath.Join(filepath.Dir(name), target)) || hasControl(target) {
				return "", refuse("The archive link %s leads out of the plugin directory.", cut(name))
			}
			if err := root.Symlink(target, name); err != nil {
				return "", refuse("The archive has %s twice or behind a link.", cut(name))
			}
		default:
			return "", refuse("The archive entry %s is not a file, directory or link.", cut(name))
		}
	}
	return pluginRoot(dir)
}

func writeEntry(root *os.Root, name string, mode fs.FileMode, from io.Reader) error {
	out, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return refuse("The archive has %s twice or behind a link.", cut(name))
	}
	if _, err := io.Copy(out, from); err != nil {
		_ = out.Close()
		return refuse("The archive is truncated.")
	}
	return out.Close()
}

// pluginRoot answers dir or its only subdirectory, whichever holds
// plugin.json.
func pluginRoot(dir string) (string, error) {
	if info, err := os.Lstat(filepath.Join(dir, ManifestFile)); err == nil && info.Mode().IsRegular() {
		return dir, nil
	}
	entries, err := os.ReadDir(dir)
	if err == nil && len(entries) == 1 && entries[0].IsDir() {
		inner := filepath.Join(dir, entries[0].Name())
		if info, err := os.Lstat(filepath.Join(inner, ManifestFile)); err == nil && info.Mode().IsRegular() {
			return inner, nil
		}
	}
	return "", refuse("The archive has no plugin.json at its top or in its single directory.")
}

// verifyDir runs the checks of a load on dir without loading it.
func verifyDir(dir string) (*Manifest, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, errors.New("The plugin directory could not be opened.")
	}
	defer root.Close()
	m, err := readManifest(root)
	if err != nil {
		return nil, err
	}
	if err := checkRun(root, m); err != nil {
		return nil, err
	}
	_, roots, err := readParts(root, m)
	for _, r := range roots {
		_ = r.Close()
	}
	return m, err
}

// Bounds of the offers waiting for an answer.
var offerTTL = 15 * time.Minute

const maxOffers = 4

// Offer is a staged release waiting for the person's yes. Replaces is the
// origin of a plugin with the same id an install would replace, Updates the
// installed version an install from the same repository updates.
type Offer struct {
	Token       string
	Repository  Repository
	ID          string
	Name        string
	Version     string
	Description string
	Archive     string
	Release     update.Release
	Replaces    string
	Updates     string

	staged  *staged
	expires time.Time
}

// Look stages the newest fitting release of the repository at raw for the
// person to confirm, installing nothing. A refusal is one sentence.
func (h *Host) Look(ctx context.Context, raw, forge string) (*Offer, error) {
	repo, err := ParseRepository(raw, forge)
	if err != nil {
		return nil, err
	}
	feed := h.feedFor(repo)
	fctx, cancel := context.WithTimeout(ctx, feedTimeout)
	rels, err := feed.Releases(fctx, true)
	cancel()
	if err != nil {
		return nil, feedRefusal(repo, err)
	}
	if len(rels) == 0 {
		return nil, refuse("%s has no release with a semver tag.", repo.Short())
	}
	pending := pendingFor(rels, "")
	if len(pending) == 0 {
		newest := rels[len(rels)-1]
		return nil, refuse("Release %s has no archive for %s/%s or any, or no checksums file.", newest.Version, runtime.GOOS, runtime.GOARCH)
	}
	rel := pending[len(pending)-1]
	s, err := h.stage(ctx, feed, rel)
	if err != nil {
		return nil, err
	}
	o := &Offer{
		Token:       randomHex(16),
		Repository:  repo,
		ID:          s.manifest.ID,
		Name:        s.manifest.Name,
		Version:     rel.Version,
		Description: s.manifest.Description,
		Archive:     s.archive,
		Release:     rel.Release(),
		staged:      s,
		expires:     time.Now().Add(offerTTL),
	}
	h.ops.Lock()
	o.Replaces, o.Updates = h.standing(o)
	h.ops.Unlock()
	if err := notNewer(o); err != nil {
		s.discard()
		return nil, err
	}
	h.keepOffer(o)
	return o, nil
}

// notNewer refuses an update offer whose release is not newer than the
// installed one.
func notNewer(o *Offer) error {
	if o.Updates == "" || update.Newer(o.Updates, o.Version) {
		return nil
	}
	return refuse("Nothing to update: %s %s is installed, the newest release of %s is %s.", o.Name, o.Updates, o.Repository.Short(), o.Version)
}

// standing answers the current Replaces and Updates of o. Under ops.
func (h *Host) standing(o *Offer) (replaces, updates string) {
	h.mu.Lock()
	e := h.entries[o.ID]
	h.mu.Unlock()
	if e == nil {
		return "", ""
	}
	rec := h.origins()[o.ID]
	if repo, err := rec.repository(); err == nil && o.Repository.Same(repo) && rec.Current != nil {
		return "", rec.Current.Version
	}
	return originLabel(rec), ""
}

func (h *Host) keepOffer(o *Offer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var dropped []*Offer
	for token, kept := range h.offers {
		if time.Now().After(kept.expires) {
			dropped = append(dropped, kept)
			delete(h.offers, token)
		}
	}
	for len(h.offers) >= maxOffers {
		var oldest *Offer
		for _, kept := range h.offers {
			if oldest == nil || kept.expires.Before(oldest.expires) {
				oldest = kept
			}
		}
		dropped = append(dropped, oldest)
		delete(h.offers, oldest.Token)
	}
	h.offers[o.Token] = o
	for _, gone := range dropped {
		gone.staged.discard()
	}
	time.AfterFunc(time.Until(o.expires), func() { h.expire(o) })
}

// expire drops an unanswered offer with its staging.
func (h *Host) expire(o *Offer) {
	h.mu.Lock()
	kept := h.offers[o.Token] == o
	if kept {
		delete(h.offers, o.Token)
	}
	h.mu.Unlock()
	if kept {
		o.staged.discard()
	}
}

func (h *Host) takeOffer(token string) *Offer {
	h.mu.Lock()
	defer h.mu.Unlock()
	o := h.offers[token]
	delete(h.offers, token)
	if o != nil && time.Now().After(o.expires) {
		o.staged.discard()
		return nil
	}
	return o
}

// dropOffers deletes every offer's staging, at Close.
func (h *Host) dropOffers() {
	h.mu.Lock()
	offers := h.offers
	h.offers = map[string]*Offer{}
	h.mu.Unlock()
	for _, o := range offers {
		o.staged.discard()
	}
}

// ErrOfferGone refuses an offer no longer kept: expired, used or lost to a
// restart.
var ErrOfferGone = refuse("The download is gone, look up the repository again.")

// ReplaceError asks before a plugin is replaced by one of the same id from
// another source. Offer, when set, is the offer to ask about again, its
// Replaces current.
type ReplaceError struct {
	Offer *Offer
	ID    string
	From  string
}

func (e *ReplaceError) Error() string {
	return fmt.Sprintf("A plugin with the id %s is installed from %s.", e.ID, e.From)
}

// Adopt installs an accepted offer, auto turns its automatic updates on.
// replaces is the Replaces the person confirmed. When Replaces or Updates
// changed since, nothing changes and a ReplaceError asks again. A newer
// release of the same repository installs as an update, the version before
// kept to go back to.
func (h *Host) Adopt(token string, auto bool, replaces string) (string, error) {
	o := h.takeOffer(token)
	if o == nil {
		return "", ErrOfferGone
	}
	h.ops.Lock()
	defer h.ops.Unlock()
	nowReplaces, nowUpdates := h.standing(o)
	changed := nowReplaces != replaces || nowUpdates != o.Updates
	o.Replaces, o.Updates = nowReplaces, nowUpdates
	if err := notNewer(o); err != nil {
		o.staged.discard()
		return "", err
	}
	if changed {
		h.keepOffer(o)
		return "", &ReplaceError{Offer: o, ID: o.ID, From: nowReplaces}
	}
	rec := &origin{Repository: o.Repository.URL, Forge: o.Repository.Forge, Auto: auto}
	if o.Updates != "" {
		if err := h.switchUpdate(o.ID, o.staged, func(r *origin) { r.Auto = auto }); err != nil {
			return "", err
		}
		return o.ID, nil
	}
	if err := h.switchFresh(o.ID, o.staged, rec); err != nil {
		return "", err
	}
	return o.ID, nil
}

// sweepLeftovers deletes the temporary entries an interrupted install,
// removal, fetch or link switch left.
func (h *Host) sweepLeftovers() {
	entries, err := os.ReadDir(h.Dir())
	if err != nil {
		return
	}
	for _, d := range entries {
		name := d.Name()
		if slices.ContainsFunc([]string{".install-", ".remove-", ".fetch-", ".link-"}, func(p string) bool { return strings.HasPrefix(name, p) }) {
			_ = os.RemoveAll(filepath.Join(h.Dir(), name))
		}
	}
}
