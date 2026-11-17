package pluginhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
	"github.com/marein/dev-cockpit/internal/update"
)

// A plugin installed from a repository is a link from <id> to
// .versions/<id>/<dir>, one directory per release. A switch is one rename of
// the link, so the folder never holds half a plugin, and the release before
// stays to go back to.
const versionsDir = ".versions"

// origin is where an external plugin came from, a repository or a host
// directory, kept by id in the host's state file. A plugin dropped into the
// folder has none. Trial marks an unsettled switch to Current, cut short by
// a restart or not run long enough yet. Failed is the last release rolled
// back from.
type origin struct {
	Repository string     `json:"repository,omitempty"`
	Forge      string     `json:"forge,omitempty"`
	Path       string     `json:"path,omitempty"`
	Auto       bool       `json:"auto,omitempty"`
	Current    *installed `json:"current,omitempty"`
	Previous   *installed `json:"previous,omitempty"`
	Trial      bool       `json:"trial,omitempty"`
	Failed     *failure   `json:"failed,omitempty"`
}

// installed is one release unpacked below .versions/<id>.
type installed struct {
	Version string `json:"version"`
	Dir     string `json:"dir"`
}

// failure is a release rolled back from, Back the version restored.
type failure struct {
	Version string   `json:"version"`
	Back    string   `json:"back"`
	Reason  string   `json:"reason"`
	Tail    []string `json:"tail,omitempty"`
}

// Update timing: first check after start, check interval, and how long a
// release must run after a switch to be kept. Variables so tests can shorten
// them.
var (
	checkFirst  = time.Minute
	checkEvery  = 6 * time.Hour
	trialSettle = 30 * time.Second
)

// checkInfo is a plugin's last update check: the newer releases, and why
// the check or an automatic update failed.
type checkInfo struct {
	pending []update.Published
	problem string
}

// same answers whether c and other found the same, so a check that finds
// nothing new triggers no page refresh.
func (c checkInfo) same(other checkInfo) bool {
	if c.problem != other.problem || len(c.pending) != len(other.pending) {
		return false
	}
	for i := range c.pending {
		if c.pending[i].Tag != other.pending[i].Tag || c.pending[i].Notes != other.pending[i].Notes {
			return false
		}
	}
	return true
}

func originLabel(rec *origin) string {
	switch {
	case rec == nil:
		return "the plugins folder"
	case rec.Repository != "":
		return strings.TrimPrefix(rec.Repository, "https://") + " on " + forgeName(rec.forge())
	default:
		return rec.Path
	}
}

// forge answers the origin's forge. An origin kept before Forge existed
// takes it from its host, the only two hosts installs took then.
func (rec *origin) forge() string {
	if rec.Forge != "" {
		return rec.Forge
	}
	switch {
	case strings.HasPrefix(strings.ToLower(rec.Repository), "https://github.com/"):
		return ForgeGitHub
	case strings.HasPrefix(strings.ToLower(rec.Repository), "https://gitlab.com/"):
		return ForgeGitLab
	}
	return ""
}

// repository parses the origin's repository, an error for an origin without
// one.
func (rec *origin) repository() (Repository, error) {
	if rec == nil || rec.Repository == "" {
		return Repository{}, errors.New("no repository")
	}
	return ParseRepository(rec.Repository, rec.forge())
}

func (h *Host) versionsOf(id string) string { return filepath.Join(h.Dir(), versionsDir, id) }

// origins reads the origins by id, empty when the file cannot be read.
func (h *Host) origins() map[string]*origin {
	var st hostState
	statefile.Load(enabledPath(h.cfg.StateDir), &st)
	if st.Origins == nil {
		st.Origins = map[string]*origin{}
	}
	return st.Origins
}

// changeState applies change to the state file and writes it when it moved.
// An unreadable file changes nothing. Under ops.
func (h *Host) changeState(change func(*hostState)) error {
	var st hostState
	if err := statefile.Read(enabledPath(h.cfg.StateDir), &st); err != nil {
		return err
	}
	before, _ := json.Marshal(st)
	if st.Origins == nil {
		st.Origins = map[string]*origin{}
	}
	change(&st)
	if len(st.Origins) == 0 {
		st.Origins = nil
	}
	if after, _ := json.Marshal(st); bytes.Equal(before, after) {
		return nil
	}
	return statefile.Write(enabledPath(h.cfg.StateDir), 0o600, st)
}

func (h *Host) setOrigin(id string, rec *origin) error {
	return h.changeState(func(st *hostState) {
		if rec == nil {
			delete(st.Origins, id)
			return
		}
		st.Origins[id] = rec
	})
}

// pointsAt answers the version directory the entry of id links to, "" for
// anything else, and whether the entry exists.
func (h *Host) pointsAt(id string) (string, bool) {
	target, err := os.Readlink(filepath.Join(h.Dir(), id))
	if err != nil {
		_, statErr := os.Lstat(filepath.Join(h.Dir(), id))
		return "", statErr == nil
	}
	prefix := versionsDir + "/" + id + "/"
	if !strings.HasPrefix(target, prefix) || strings.Contains(strings.TrimPrefix(target, prefix), "/") {
		return "", true
	}
	return strings.TrimPrefix(target, prefix), true
}

// point links the entry of id to its version directory dir in one rename. A
// plain directory there, a plugin of another source, is moved aside first
// and deleted after.
func (h *Host) point(id, dir string) error {
	entry := filepath.Join(h.Dir(), id)
	link := filepath.Join(h.Dir(), ".link-"+randomHex(6))
	if err := os.Symlink(filepath.Join(versionsDir, id, dir), link); err != nil {
		return err
	}
	aside := ""
	if info, err := os.Lstat(entry); err == nil && info.Mode()&fs.ModeSymlink == 0 {
		aside = filepath.Join(h.Dir(), ".remove-"+randomHex(6))
		if err := os.Rename(entry, aside); err != nil {
			_ = os.Remove(link)
			return err
		}
	}
	if err := os.Rename(link, entry); err != nil {
		_ = os.Remove(link)
		if aside != "" {
			_ = os.Rename(aside, entry)
		}
		return err
	}
	if aside != "" {
		_ = os.RemoveAll(aside)
	}
	return nil
}

// keepVersions deletes the version directories of id except keep and the
// link target. The link is what runs, an origin comes from a file that can
// be lost.
func (h *Host) keepVersions(id string, keep ...string) {
	if at, _ := h.pointsAt(id); at != "" {
		keep = append(keep, at)
	}
	entries, err := os.ReadDir(h.versionsOf(id))
	if err != nil {
		return
	}
	for _, d := range entries {
		if !slices.Contains(keep, d.Name()) {
			_ = os.RemoveAll(filepath.Join(h.versionsOf(id), d.Name()))
		}
	}
	if len(keep) == 0 {
		_ = os.Remove(h.versionsOf(id))
	}
}

// adopt moves a staged release below the plugin's versions and answers it.
func (h *Host) adopt(id string, s *staged) (*installed, error) {
	if err := os.MkdirAll(h.versionsOf(id), 0o700); err != nil {
		return nil, err
	}
	in := &installed{Version: s.release.Version, Dir: s.release.Version + "-" + randomHex(3)}
	if err := os.Rename(s.root, filepath.Join(h.versionsOf(id), in.Dir)); err != nil {
		return nil, err
	}
	s.discard()
	return in, nil
}

// switchFresh installs a staged release as id with nothing to go back to,
// replacing whatever is there, and switches it on. Under ops.
func (h *Host) switchFresh(id string, s *staged, rec *origin) error {
	in, err := h.adopt(id, s)
	if err != nil {
		s.discard()
		return err
	}
	rec.Current, rec.Trial = in, true
	if err := h.changeState(func(st *hostState) {
		st.Origins[id] = rec
		st.Disabled = slices.DeleteFunc(st.Disabled, func(listed string) bool { return listed == id })
	}); err != nil {
		h.keepVersions(id, h.keptDirs(id)...)
		return err
	}
	h.mu.Lock()
	old := h.entries[id]
	h.mu.Unlock()
	if old != nil {
		h.unload(old)
	}
	if err := h.point(id, in.Dir); err != nil {
		log.Printf("plugin %s: the link to %s could not be made: %v", id, in.Version, err)
	}
	h.keepVersions(id, in.Dir)
	rec.Trial = false
	if err := h.setOrigin(id, rec); err != nil {
		log.Printf("plugin %s: installed, but its origin could not be kept: %v", id, err)
	}
	h.clearCheck(id)
	h.reload(id)
	h.changed()
	return nil
}

// switchUpdate makes a staged release current, keeps the one before to go
// back to and starts the trial. change adjusts the origin. Under ops.
func (h *Host) switchUpdate(id string, s *staged, change func(*origin)) error {
	rec := h.origins()[id]
	if rec == nil || rec.Current == nil {
		s.discard()
		return refuse("The plugin %s changed during the download, nothing was installed.", id)
	}
	in, err := h.adopt(id, s)
	if err != nil {
		s.discard()
		return err
	}
	rec.Previous, rec.Current, rec.Trial, rec.Failed = rec.Current, in, true, nil
	if change != nil {
		change(rec)
	}
	if err := h.setOrigin(id, rec); err != nil {
		h.keepVersions(id, h.keptDirs(id)...)
		return err
	}
	h.mu.Lock()
	old := h.entries[id]
	h.mu.Unlock()
	if old != nil {
		h.unload(old)
	}
	if err := h.point(id, in.Dir); err != nil {
		log.Printf("plugin %s: the link to %s could not be made: %v", id, in.Version, err)
	}
	h.keepVersions(id, in.Dir, rec.Previous.Dir)
	h.clearCheck(id)
	h.reload(id)
	h.changed()
	h.beginTrial(id, in.Dir)
	return nil
}

// keptDirs answers the version directories the origin of id names.
func (h *Host) keptDirs(id string) []string {
	rec := h.origins()[id]
	if rec == nil {
		return nil
	}
	var dirs []string
	for _, in := range []*installed{rec.Current, rec.Previous} {
		if in != nil {
			dirs = append(dirs, in.Dir)
		}
	}
	return dirs
}

// beginTrial watches the release in dir after a switch. Running for
// trialSettle keeps it, crashing or turning broken before rolls it back.
func (h *Host) beginTrial(id, dir string) {
	h.mu.Lock()
	if h.trials[id] == dir {
		h.mu.Unlock()
		return
	}
	h.trials[id] = dir
	h.mu.Unlock()
	h.watching.Add(1)
	go func() {
		defer h.watching.Done()
		defer func() {
			h.mu.Lock()
			if h.trials[id] == dir {
				delete(h.trials, id)
			}
			h.mu.Unlock()
		}()
		h.trial(id, dir)
	}()
}

func (h *Host) trial(id, dir string) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.Now().Add(startBudget + trialSettle + time.Minute)
	var since time.Time
	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
		}
		h.mu.Lock()
		e := h.entries[id]
		var st Status
		if e != nil {
			st = h.status(e, nil)
		}
		h.mu.Unlock()
		switch {
		case e == nil:
			return
		case st.State == StateCrashed || st.State == StateBroken:
			h.rollback(id, dir, st.Reason, st.Tail)
			return
		case st.State == StateDisabled || time.Now().After(deadline):
			h.settle(id, dir)
			return
		case st.State == StateRunning:
			if since.IsZero() {
				since = time.Now()
			}
			if time.Since(since) >= trialSettle {
				h.settle(id, dir)
				return
			}
		}
	}
}

// settle keeps the release in dir once its trial is over.
func (h *Host) settle(id, dir string) {
	h.ops.Lock()
	defer h.ops.Unlock()
	rec := h.origins()[id]
	if rec == nil || !rec.Trial || rec.Current == nil || rec.Current.Dir != dir {
		return
	}
	rec.Trial, rec.Failed = false, nil
	if err := h.setOrigin(id, rec); err != nil {
		log.Printf("plugin %s: %s ran, but that could not be kept: %v", id, rec.Current.Version, err)
	}
	h.changed()
}

// rollback restores the release before the one in dir, which failed its
// trial for reason, and records why. The failed release is deleted, so
// nothing flips back and forth.
func (h *Host) rollback(id, dir, reason string, tail []string) {
	h.ops.Lock()
	defer h.ops.Unlock()
	rec := h.origins()[id]
	if rec == nil || !rec.Trial || rec.Current == nil || rec.Current.Dir != dir {
		return
	}
	failed, back := rec.Current, rec.Previous
	if back == nil {
		rec.Trial = false
		_ = h.setOrigin(id, rec)
		return
	}
	if _, err := os.Stat(filepath.Join(h.versionsOf(id), back.Dir)); err != nil {
		log.Printf("plugin %s: %s failed and %s to go back to is gone, it stays", id, failed.Version, back.Version)
		rec.Trial, rec.Previous = false, nil
		_ = h.setOrigin(id, rec)
		return
	}
	if reason == "" {
		reason = "It did not start."
	}
	rec.Current, rec.Previous, rec.Trial = back, nil, false
	rec.Failed = &failure{Version: failed.Version, Back: back.Version, Reason: reason, Tail: tail}
	if err := h.setOrigin(id, rec); err != nil {
		log.Printf("plugin %s: %s failed, but going back could not be kept: %v", id, failed.Version, err)
		return
	}
	log.Printf("plugin %s: %s failed (%s), going back to %s", id, failed.Version, reason, back.Version)
	h.mu.Lock()
	e := h.entries[id]
	h.mu.Unlock()
	if e != nil {
		h.unload(e)
	}
	if err := h.point(id, back.Dir); err != nil {
		log.Printf("plugin %s: the link back to %s could not be made: %v", id, back.Version, err)
	}
	h.keepVersions(id, back.Dir)
	h.clearCheck(id)
	h.reload(id)
	h.changed()
}

// recover aligns the folder with the origins at start. An install, update or
// rollback cut short is finished or undone, every link points at its
// origin's release, and a trial resumes once the host runs. An entry removed
// or replaced by hand loses its origin. Under ops, before the first scan.
func (h *Host) recover() {
	h.sweepLeftovers()
	err := h.changeState(func(st *hostState) {
		for id, rec := range st.Origins {
			if !ValidID(id) {
				delete(st.Origins, id)
				continue
			}
			if rec.Repository == "" {
				if _, there := h.pointsAt(id); !there {
					delete(st.Origins, id)
				}
				continue
			}
			if !h.recoverRepository(id, rec) {
				delete(st.Origins, id)
				h.keepVersions(id)
			}
		}
		h.dropStrayVersions(st.Origins)
	})
	if err != nil {
		log.Printf("plugins: the origins could not be read, nothing was recovered: %v", err)
	}
}

// recoverRepository repairs one repository plugin at start and answers
// whether it is kept.
func (h *Host) recoverRepository(id string, rec *origin) bool {
	exists := func(in *installed) bool {
		if in == nil || in.Dir == "" || strings.ContainsAny(in.Dir, `/\`) {
			return false
		}
		_, err := os.Stat(filepath.Join(h.versionsOf(id), in.Dir))
		return err == nil
	}
	if !exists(rec.Previous) {
		rec.Previous = nil
	}
	if !exists(rec.Current) {
		if rec.Previous == nil {
			return false
		}
		rec.Current, rec.Previous, rec.Trial = rec.Previous, nil, false
	}
	at, _ := h.pointsAt(id)
	switch {
	case at == rec.Current.Dir:
	case at != "", rec.Trial:
		if err := h.point(id, rec.Current.Dir); err != nil {
			log.Printf("plugin %s: the link to %s could not be made again: %v", id, rec.Current.Version, err)
		}
	default:
		return false
	}
	if rec.Trial && rec.Previous == nil {
		rec.Trial = false
	}
	if rec.Trial {
		h.mu.Lock()
		h.resume = append(h.resume, [2]string{id, rec.Current.Dir})
		h.mu.Unlock()
	}
	keep := []string{rec.Current.Dir}
	if rec.Previous != nil {
		keep = append(keep, rec.Previous.Dir)
	}
	h.keepVersions(id, keep...)
	return true
}

// dropStrayVersions deletes the versions of plugins no origin names. No
// origins at all may mean a lost, empty or broken state file, so then it
// deletes nothing.
func (h *Host) dropStrayVersions(origins map[string]*origin) {
	if len(origins) == 0 {
		return
	}
	entries, err := os.ReadDir(filepath.Join(h.Dir(), versionsDir))
	if err != nil {
		return
	}
	for _, d := range entries {
		if rec := origins[d.Name()]; rec == nil || rec.Repository == "" {
			h.keepVersions(d.Name())
		}
	}
}

// forget drops the origin and versions of a plugin whose entry was removed
// or changed by hand. Under ops.
func (h *Host) forget() {
	origins := h.origins()
	var gone []string
	for id, rec := range origins {
		at, there := h.pointsAt(id)
		if !there || rec.Repository != "" && (rec.Current == nil || at != rec.Current.Dir) {
			gone = append(gone, id)
		}
	}
	if len(gone) == 0 {
		return
	}
	err := h.changeState(func(st *hostState) {
		for _, id := range gone {
			delete(st.Origins, id)
		}
	})
	if err != nil {
		return
	}
	for _, id := range gone {
		h.keepVersions(id)
		h.clearCheck(id)
	}
}

func (h *Host) feedFor(repo Repository) *update.Feed {
	h.mu.Lock()
	defer h.mu.Unlock()
	key := repo.Forge + " " + strings.ToLower(repo.URL)
	if f, ok := h.feeds[key]; ok {
		return f
	}
	if len(h.feeds) >= 2*maxPlugins {
		clear(h.feeds)
	}
	f := repo.feed(h.client)
	h.feeds[key] = f
	return f
}

func (h *Host) clearCheck(id string) {
	h.mu.Lock()
	delete(h.checks, id)
	delete(h.refused, id)
	h.mu.Unlock()
}

// repositoryOf answers the origin and repository of a plugin installed from
// one, a refusal for any other.
func (h *Host) repositoryOf(id string) (*origin, Repository, error) {
	if _, err := h.known(id); err != nil {
		return nil, Repository{}, err
	}
	rec := h.origins()[id]
	if rec == nil || rec.Repository == "" || rec.Current == nil {
		return nil, Repository{}, refuse("The plugin %s was not installed from a repository and has no updates.", id)
	}
	repo, err := rec.repository()
	if err != nil {
		return nil, Repository{}, refuse("The plugin %s has no readable repository.", id)
	}
	return rec, repo, nil
}

// Update installs the newest release of the plugin's repository. version is
// the one the person saw, "" for any. A newer one is refused, so nobody
// installs notes they never read. The release before stays, and one that
// fails its trial is rolled back.
func (h *Host) Update(ctx context.Context, id, version string) error {
	rec, repo, err := h.repositoryOf(id)
	if err != nil {
		return err
	}
	if !h.claimBusy(id, "Checking for the newest release.") {
		return refuse("The plugin %s is already updating.", id)
	}
	defer h.releaseBusy(id)
	feed := h.feedFor(repo)
	fctx, cancel := context.WithTimeout(ctx, feedTimeout)
	rels, err := feed.Releases(fctx, true)
	cancel()
	if err != nil {
		return feedRefusal(repo, err)
	}
	pending := pendingFor(rels, rec.Current.Version)
	h.mu.Lock()
	h.checks[id] = checkInfo{pending: pending}
	h.mu.Unlock()
	if len(pending) == 0 {
		h.changed()
		return refuse("%s is the newest release.", rec.Current.Version)
	}
	newest := pending[len(pending)-1]
	if version != "" && !update.SameVersion(newest.Version, version) {
		h.changed()
		return refuse("Release %s is out meanwhile, see its notes on the row.", newest.Version)
	}
	h.setBusy(id, "Downloading "+newest.Version+".")
	s, err := h.stage(ctx, feed, newest)
	if err != nil {
		return err
	}
	if s.manifest.ID != id {
		s.discard()
		return refuse("Release %s refused: its plugin.json says id %s.", newest.Version, s.manifest.ID)
	}
	h.ops.Lock()
	defer h.ops.Unlock()
	now := h.origins()[id]
	if nowRepo, err := now.repository(); err != nil || !nowRepo.Same(repo) || now.Current == nil || now.Current.Dir != rec.Current.Dir {
		s.discard()
		return refuse("The plugin %s changed during the download, nothing was installed.", id)
	}
	return h.switchUpdate(id, s, nil)
}

// SetAuto switches automatic updates of a repository plugin. Switching on
// installs a newer release already known.
func (h *Host) SetAuto(id string, on bool) error {
	h.ops.Lock()
	rec, _, err := h.repositoryOf(id)
	if err == nil {
		rec.Auto = on
		err = h.setOrigin(id, rec)
	}
	h.ops.Unlock()
	if err != nil {
		return err
	}
	h.changed()
	if on {
		h.mu.Lock()
		info := h.checks[id]
		h.mu.Unlock()
		if version, ok := h.wantsAuto(id, rec, info.pending); ok {
			h.watching.Add(1)
			go func() {
				defer h.watching.Done()
				h.autoUpdate(id, version)
			}()
		}
	}
	return nil
}

// wantsAuto answers the newest pending release when an automatic update
// should install it: not a failed release or an older one, nor one refused
// since the start.
func (h *Host) wantsAuto(id string, rec *origin, pending []update.Published) (string, bool) {
	if rec == nil || !rec.Auto || rec.Trial || len(pending) == 0 {
		return "", false
	}
	newest := pending[len(pending)-1].Version
	h.mu.Lock()
	refused := h.refused[id]
	h.mu.Unlock()
	if refused != "" && update.SameVersion(refused, newest) {
		return "", false
	}
	return newest, rec.Failed == nil || update.Newer(rec.Failed.Version, newest)
}

// autoUpdate installs version unattended. A refused release is not tried
// again until a newer one is out, the row says why.
func (h *Host) autoUpdate(id, version string) {
	ctx, cancel := h.stopContext(fetchTimeout + feedTimeout)
	defer cancel()
	err := h.Update(ctx, id, version)
	if err == nil {
		return
	}
	message := "The automatic update failed."
	if IsInstallError(err) {
		message = "The automatic update failed: " + err.Error()
	}
	log.Printf("plugin %s: %s", id, message)
	h.mu.Lock()
	h.refused[id] = version
	info := h.checks[id]
	info.problem = message
	h.checks[id] = info
	h.mu.Unlock()
	h.changed()
}

// CheckUpdates reads the feed of every repository plugin, keeps the newer
// releases for its row and installs them where automatic updates are on.
// Each feed is asked with the ETag of its last answer, so an unchanged feed
// costs no rate limit.
func (h *Host) CheckUpdates(ctx context.Context) {
	h.checking.Lock()
	defer h.checking.Unlock()
	origins := h.origins()
	var auto [][2]string
	moved := false
	for _, id := range sortedKeys(origins) {
		rec := origins[id]
		if rec.Repository == "" || rec.Current == nil {
			continue
		}
		if _, err := h.known(id); err != nil {
			continue
		}
		repo, err := rec.repository()
		if err != nil {
			continue
		}
		fctx, cancel := context.WithTimeout(ctx, feedTimeout)
		rels, err := h.feedFor(repo).Releases(fctx, true)
		cancel()
		info := checkInfo{pending: pendingFor(rels, rec.Current.Version)}
		if err != nil {
			info.problem = "The update check failed: " + feedRefusal(repo, err).Error()
		}
		h.mu.Lock()
		if refused := h.refused[id]; err == nil && refused != "" && len(info.pending) > 0 && update.SameVersion(refused, info.pending[len(info.pending)-1].Version) {
			info.problem = h.checks[id].problem
		}
		h.mu.Unlock()
		h.mu.Lock()
		moved = moved || !info.same(h.checks[id])
		h.checks[id] = info
		h.mu.Unlock()
		if version, ok := h.wantsAuto(id, rec, info.pending); ok {
			auto = append(auto, [2]string{id, version})
		}
	}
	if moved {
		h.changed()
	}
	for _, next := range auto {
		h.autoUpdate(next[0], next[1])
	}
}

func (h *Host) checkLoop() {
	defer h.watching.Done()
	timer := time.NewTimer(min(checkFirst, h.every()))
	defer timer.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-timer.C:
			ctx, cancel := h.stopContext(time.Hour)
			h.CheckUpdates(ctx)
			cancel()
			timer.Reset(h.every())
		}
	}
}

// stopContext is a context that ends after d or when the host closes.
func (h *Host) stopContext(d time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), d)
	go func() {
		select {
		case <-h.stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func (h *Host) every() time.Duration {
	if h.cfg.CheckEvery > 0 {
		return h.cfg.CheckEvery
	}
	return checkEvery
}

func (h *Host) claimBusy(id, doing string) bool {
	h.mu.Lock()
	if _, ok := h.busy[id]; ok {
		h.mu.Unlock()
		return false
	}
	h.busy[id] = doing
	h.mu.Unlock()
	h.changed()
	return true
}

func (h *Host) setBusy(id, doing string) {
	h.mu.Lock()
	h.busy[id] = doing
	h.mu.Unlock()
	h.changed()
}

func (h *Host) releaseBusy(id string) {
	h.mu.Lock()
	delete(h.busy, id)
	h.mu.Unlock()
	h.changed()
}

// Origin is where a plugin came from and its update state, as its row shows
// it. Trial is the version on trial, Busy the update step in progress,
// Problem why the last check or automatic update failed.
type Origin struct {
	Repository string
	Forge      string
	Path       string
	Auto       bool
	Trial      string
	Step       *Step
	Busy       string
	Failed     *Failed
	Problem    string
}

// Source is the repository's host and path.
func (o Origin) Source() string { return strings.TrimPrefix(o.Repository, "https://") }

// ForgeName is the forge the releases come from.
func (o Origin) ForgeName() string { return forgeName(o.Forge) }

// Step leads from the installed release to the newest, Releases newest
// first.
type Step struct {
	From     string
	To       string
	Releases []update.Release
}

// Failed is the release rolled back from after an update.
type Failed struct {
	Version string
	Back    string
	Reason  string
	Tail    []string
}

// originOf builds the row's Origin. Under mu.
func (h *Host) originOf(id string, rec *origin) Origin {
	o := Origin{Busy: h.busy[id]}
	if rec == nil {
		return o
	}
	o.Repository, o.Path, o.Auto = rec.Repository, rec.Path, rec.Auto
	if rec.Repository != "" {
		o.Forge = rec.forge()
	}
	if rec.Trial && rec.Current != nil {
		o.Trial = rec.Current.Version
	}
	if f := rec.Failed; f != nil {
		o.Failed = &Failed{Version: f.Version, Back: f.Back, Reason: f.Reason, Tail: f.Tail}
	}
	info := h.checks[id]
	o.Problem = info.problem
	if len(info.pending) > 0 && rec.Current != nil {
		step := &Step{From: rec.Current.Version, To: info.pending[len(info.pending)-1].Version}
		for i := len(info.pending) - 1; i >= 0; i-- {
			step.Releases = append(step.Releases, info.pending[i].Release())
		}
		o.Step = step
	}
	return o
}
