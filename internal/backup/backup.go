// Package backup exports selected cockpit and host state into one archive
// and applies such an archive back, so a freshly set up server continues
// where the old one stopped. The archive is a tar.gz with a manifest, the
// import adapts to whatever sections the file contains. An optional password
// wraps the stream into framed AES-256-GCM, see crypt.go.
package backup

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/local/dev-cockpit/internal/statefile"
)

const (
	manifestName = "manifest.json"
	appMarker    = "dev-cockpit-backup"
	// pendingTTL is how long an uploaded archive waits for the apply step.
	pendingTTL = time.Hour
)

// Source maps one archive key to one host path. Name is a single path
// component below data/<section>/ in the archive, Path is the absolute host
// location, a file or a directory. The mapping is resolved against the
// current registry on import, so an archive stays portable across homes.
type Source struct {
	Name string
	Path string
}

// Section is one selectable unit of the export and import.
type Section struct {
	ID          string
	Label       string
	Description string
	Group       string
	Sources     []Source
}

// SectionView is the UI facing shape of a section.
type SectionView struct {
	ID          string
	Label       string
	Description string
	Available   bool
}

// GroupView groups sections for the settings page.
type GroupView struct {
	Label    string
	Sections []SectionView
}

// ManifestSection records one exported section with its footprint.
type ManifestSection struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
}

// Manifest identifies a backup archive and lists what it contains.
type Manifest struct {
	App        string            `json:"app"`
	Format     int               `json:"format"`
	AppVersion string            `json:"appVersion"`
	CreatedAt  time.Time         `json:"createdAt"`
	Host       string            `json:"host"`
	Sections   []ManifestSection `json:"sections"`
}

// ApplyResult summarizes one import run.
type ApplyResult struct {
	Sections    int
	Files       int
	Skipped     int
	Overwritten int
}

// Service holds the section registry, the pending import store, and the
// overwrite review list.
type Service struct {
	stateDir string
	version  string
	sections []Section
	mu       sync.Mutex
}

// New builds the service for the given state and projects directories, so
// an import always lands in the running server's current paths. Host
// sections resolve against the current user's home directory.
func New(stateDir, projectsDir, version string) *Service {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("backup: resolve home: %v", err)
	}
	return newService(stateDir, projectsDir, home, version)
}

func newService(stateDir, projectsDir, home, version string) *Service {
	return &Service{stateDir: stateDir, version: version, sections: buildSections(stateDir, projectsDir, home)}
}

func buildSections(stateDir, projectsDir, home string) []Section {
	st := func(name string) string { return filepath.Join(stateDir, name) }
	hm := func(name string) string { return filepath.Join(home, name) }
	return []Section{
		{ID: "settings", Group: "Cockpit", Label: "Settings",
			Description: "General settings and the notification jingle.",
			Sources:     []Source{{"settings.json", st("settings.json")}}},
		{ID: "recents", Group: "Cockpit", Label: "Recent projects",
			Description: "Project usage order for the lists and pickers.",
			Sources:     []Source{{"recent-projects.json", st("recent-projects.json")}}},
		{ID: "push", Group: "Cockpit", Label: "Push channels",
			Description: "Web push devices, webhooks, the VAPID keys and the base URL. Devices keep ringing without a new registration as long as Dev Cockpit keeps its address, under a new address every device must register again. The keys let anyone holding this file push to the devices, and an old installation left running rings the same phones twice.",
			Sources: []Source{
				{"push-vapid.json", st("push-vapid.json")},
				{"push-channels.json", st("push-channels.json")},
				{"push-subscriptions.json", st("push-subscriptions.json")}}},
		{ID: "terminals", Group: "Cockpit", Label: "Terminal snapshot",
			Description: "The restore snapshot. With the restore setting on, coders and shells come back on the next start.",
			Sources:     []Source{{"terminal-restore.json", st("terminal-restore.json")}}},
		{ID: "shell-history", Group: "Cockpit", Label: "Shell histories",
			Description: "Per shell command history files.",
			Sources:     []Source{{"shell-history", st("shell-history")}}},

		{ID: "claude", Group: "Coders", Label: "Claude config",
			Description: "Instructions, agents, skills, settings and the login credentials.",
			Sources: []Source{
				{"CLAUDE.md", hm(".claude/CLAUDE.md")},
				{"settings.json", hm(".claude/settings.json")},
				{"settings.local.json", hm(".claude/settings.local.json")},
				{"keybindings.json", hm(".claude/keybindings.json")},
				{"credentials.json", hm(".claude/.credentials.json")},
				{"statusline.sh", hm(".claude/statusline.sh")},
				{"agents", hm(".claude/agents")},
				{"skills", hm(".claude/skills")},
				{"commands", hm(".claude/commands")},
				{"hooks", hm(".claude/hooks")}}},
		{ID: "claude-sessions", Group: "Coders", Label: "Claude sessions",
			Description: "Session transcripts and prompt history. Resuming from the snapshot needs them. Can be large.",
			Sources: []Source{
				{"projects", hm(".claude/projects")},
				{"history.jsonl", hm(".claude/history.jsonl")}}},
		{ID: "copilot", Group: "Coders", Label: "Copilot config",
			Description: "Instructions, agents, skills, settings and the login.",
			Sources: []Source{
				{"copilot-instructions.md", hm(".copilot/copilot-instructions.md")},
				{"settings.json", hm(".copilot/settings.json")},
				{"config.json", hm(".copilot/config.json")},
				{"agents", hm(".copilot/agents")},
				{"skills", hm(".copilot/skills")}}},
		{ID: "copilot-sessions", Group: "Coders", Label: "Copilot sessions",
			Description: "Session state and history. Can be large.",
			Sources: []Source{
				{"session-state", hm(".copilot/session-state")},
				{"session-store.db", hm(".copilot/session-store.db")},
				{"session-store.db-wal", hm(".copilot/session-store.db-wal")},
				{"session-store.db-shm", hm(".copilot/session-store.db-shm")},
				{"command-history-state.json", hm(".copilot/command-history-state.json")}}},

		{ID: "projects", Group: "Host", Label: "Projects",
			Description: "The complete projects directory, every working copy as it sits on disk. Imports into the current projects dir. Can be large.",
			Sources:     []Source{{"projects", projectsDir}}},
		{ID: "ssh", Group: "Host", Label: "SSH keys",
			Description: "The complete ~/.ssh, keys, config, known_hosts, authorized_keys. File modes are preserved.",
			Sources:     []Source{{"ssh", hm(".ssh")}}},
		{ID: "git", Group: "Host", Label: "Git config",
			Description: "Global identity, aliases and helpers, ~/.gitconfig and ~/.config/git.",
			Sources: []Source{
				{"gitconfig", hm(".gitconfig")},
				{"config-git", hm(".config/git")}}},
		{ID: "git-clis", Group: "Host", Label: "gh and glab logins",
			Description: "GitHub and GitLab CLI configuration including their tokens.",
			Sources: []Source{
				{"gh", hm(".config/gh")},
				{"glab-cli", hm(".config/glab-cli")}}},
		{ID: "docker", Group: "Host", Label: "Docker login",
			Description: "~/.docker/config.json with the registry logins, plus the docker contexts. A login held by a credential helper sits outside this file and does not travel.",
			Sources: []Source{
				{"config.json", hm(".docker/config.json")},
				{"contexts", hm(".docker/contexts")}}},
		{ID: "dotfiles", Group: "Host", Label: "Shell dotfiles",
			Description: "bash, readline, tmux and vim setup files from the home directory.",
			Sources: []Source{
				{"bashrc", hm(".bashrc")},
				{"bash_profile", hm(".bash_profile")},
				{"profile", hm(".profile")},
				{"bash_aliases", hm(".bash_aliases")},
				{"bash_logout", hm(".bash_logout")},
				{"inputrc", hm(".inputrc")},
				{"tmux.conf", hm(".tmux.conf")},
				{"config-tmux", hm(".config/tmux")},
				{"vimrc", hm(".vimrc")}}},
	}
}

func (s *Service) section(id string) *Section {
	for i := range s.sections {
		if s.sections[i].ID == id {
			return &s.sections[i]
		}
	}
	return nil
}

// Section returns the UI view of one registered section.
func (s *Service) Section(id string) (SectionView, bool) {
	sec := s.section(id)
	if sec == nil {
		return SectionView{}, false
	}
	return SectionView{ID: sec.ID, Label: sec.Label, Description: sec.Description, Available: s.available(*sec)}, true
}

// Groups returns the registry grouped for the settings page, with a cheap
// availability probe per section (stat only, no directory walks).
func (s *Service) Groups() []GroupView {
	var out []GroupView
	for _, sec := range s.sections {
		view := SectionView{ID: sec.ID, Label: sec.Label, Description: sec.Description, Available: s.available(sec)}
		if len(out) == 0 || out[len(out)-1].Label != sec.Group {
			out = append(out, GroupView{Label: sec.Group})
		}
		out[len(out)-1].Sections = append(out[len(out)-1].Sections, view)
	}
	return out
}

func (s *Service) available(sec Section) bool {
	for _, src := range sec.Sources {
		if _, err := os.Lstat(src.Path); err == nil {
			return true
		}
	}
	return false
}

// Known filters ids down to registered section ids.
func (s *Service) Known(ids []string) []string {
	var out []string
	for _, id := range ids {
		if s.section(id) != nil {
			out = append(out, id)
		}
	}
	return out
}

// Export streams a tar.gz with the selected sections and a trailing manifest
// into w. Absent sources are skipped silently, that is what keeps the
// archive adaptive.
func (s *Service) Export(w io.Writer, ids []string) error {
	known := s.Known(ids)
	if len(known) == 0 {
		return errors.New("no known sections selected")
	}
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	host, _ := os.Hostname()
	m := Manifest{App: appMarker, Format: 1, AppVersion: s.version, CreatedAt: time.Now().UTC(), Host: host}
	for _, id := range known {
		sec := s.section(id)
		entry := ManifestSection{ID: sec.ID, Label: sec.Label}
		for _, src := range sec.Sources {
			if err := collectPath(tw, "data/"+sec.ID+"/"+src.Name, src.Path, &entry); err != nil {
				return err
			}
		}
		if entry.Files > 0 {
			m.Sections = append(m.Sections, entry)
		}
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := writeTarFile(tw, manifestName, data, 0o644); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func collectPath(tw *tar.Writer, key, root string, entry *ManifestSection) error {
	info, err := os.Lstat(root)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		return addEntry(tw, key, root, info, entry)
	}
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Printf("backup: walk %s: %v", p, err)
			return nil
		}
		if strings.HasSuffix(d.Name(), preImportSuffix) {
			return nil
		}
		name := key
		if rel, err := filepath.Rel(root, p); err == nil && rel != "." {
			name = key + "/" + filepath.ToSlash(rel)
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		return addEntry(tw, name, p, fi, entry)
	})
}

func addEntry(tw *tar.Writer, name, p string, fi fs.FileInfo, entry *ManifestSection) error {
	mode := int64(fi.Mode().Perm())
	switch {
	case fi.IsDir():
		return tw.WriteHeader(&tar.Header{Name: name + "/", Typeflag: tar.TypeDir, Mode: mode, ModTime: fi.ModTime()})
	case fi.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(p)
		if err != nil {
			return nil
		}
		entry.Files++
		return tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeSymlink, Linkname: target, Mode: mode, ModTime: fi.ModTime()})
	case fi.Mode().IsRegular():
		// Read the whole file first, several sources are live files and the
		// tar header must match the byte count exactly.
		data, err := os.ReadFile(p)
		if err != nil {
			log.Printf("backup: read %s: %v", p, err)
			return nil
		}
		entry.Files++
		entry.Bytes += int64(len(data))
		return writeTarFile(tw, name, data, fs.FileMode(mode))
	default:
		// Sockets and pipes cannot travel in a backup.
		return nil
	}
}

func writeTarFile(tw *tar.Writer, name string, data []byte, mode fs.FileMode) error {
	hdr := &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: int64(mode.Perm()), Size: int64(len(data)), ModTime: time.Now()}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

var pendingIDPattern = regexp.MustCompile(`^[0-9a-f]{16,32}$`)

func (s *Service) pendingDir() string { return filepath.Join(s.stateDir, "import-pending") }

func (s *Service) pendingPath(id string) (string, error) {
	if !pendingIDPattern.MatchString(id) {
		return "", errors.New("invalid import token")
	}
	return filepath.Join(s.pendingDir(), id+".tar.gz"), nil
}

// SavePending stores an uploaded archive decrypted for the apply step and
// validates it by reading the manifest. It returns the pending id. Besides
// tar.gz and the encrypted container it accepts a plain tar, macOS Safari
// gunzips a downloaded .tar.gz on its own, and normalizes it back to tar.gz
// while storing.
func (s *Service) SavePending(r io.Reader, password string) (string, error) {
	br := bufio.NewReader(r)
	head, err := br.Peek(len(cryptMagic))
	if err != nil {
		return "", errors.New("This is not a dev-cockpit backup file.")
	}
	var src io.Reader = br
	plainTar := false
	switch {
	case string(head) == cryptMagic:
		if password == "" {
			return "", errors.New("This backup is encrypted, the password is required.")
		}
		src, err = newDecryptReader(br, password)
		if err != nil {
			return "", err
		}
	case head[0] == 0x1f && head[1] == 0x8b:
	default:
		tarHead, err := br.Peek(262)
		if err != nil || string(tarHead[257:262]) != "ustar" {
			return "", errors.New("This is not a dev-cockpit backup file.")
		}
		plainTar = true
	}
	if err := os.MkdirAll(s.pendingDir(), 0o700); err != nil {
		return "", err
	}
	id := statefile.NewID()
	dest, err := s.pendingPath(id)
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	var copyErr, closeErr error
	if plainTar {
		gw := gzip.NewWriter(f)
		_, copyErr = io.Copy(gw, src)
		if err := gw.Close(); copyErr == nil {
			copyErr = err
		}
	} else {
		_, copyErr = io.Copy(f, src)
	}
	closeErr = f.Close()
	if copyErr != nil || closeErr != nil {
		os.Remove(dest)
		if copyErr != nil {
			return "", copyErr
		}
		return "", closeErr
	}
	if _, err := s.Inspect(id); err != nil {
		os.Remove(dest)
		return "", err
	}
	return id, nil
}

// Inspect reads the manifest of a pending archive.
func (s *Service) Inspect(id string) (*Manifest, error) {
	pending, err := s.pendingPath(id)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(pending)
	if err != nil {
		return nil, errors.New("The uploaded archive is gone, upload it again.")
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, errors.New("This is not a dev-cockpit backup file.")
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, errors.New("This is not a dev-cockpit backup file.")
		}
		if path.Clean(hdr.Name) != manifestName {
			continue
		}
		var m Manifest
		if err := json.NewDecoder(io.LimitReader(tr, 1<<20)).Decode(&m); err != nil || m.App != appMarker {
			return nil, errors.New("This is not a dev-cockpit backup file.")
		}
		return &m, nil
	}
	return nil, errors.New("The archive carries no manifest, this is not a dev-cockpit backup file.")
}

// Discard drops a pending archive.
func (s *Service) Discard(id string) {
	if pending, err := s.pendingPath(id); err == nil {
		os.Remove(pending)
	}
}

// CleanupPending drops pending archives older than the TTL.
func (s *Service) CleanupPending() {
	entries, err := os.ReadDir(s.pendingDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < pendingTTL {
			continue
		}
		os.Remove(filepath.Join(s.pendingDir(), e.Name()))
	}
}

// Apply extracts the selected sections of a pending archive onto the host.
// Targets come from the current registry, never from the archive, and every
// file write verifies its real parent directory stays under the section
// source, so a crafted archive cannot escape through .. or symlinks.
func (s *Service) Apply(id string, ids []string) (ApplyResult, error) {
	var res ApplyResult
	want := map[string]bool{}
	for _, secID := range s.Known(ids) {
		want[secID] = true
	}
	if len(want) == 0 {
		return res, errors.New("Select at least one section.")
	}
	pending, err := s.pendingPath(id)
	if err != nil {
		return res, err
	}
	f, err := os.Open(pending)
	if err != nil {
		return res, errors.New("The uploaded archive is gone, upload it again.")
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return res, errors.New("This is not a dev-cockpit backup file.")
	}
	tr := tar.NewReader(gz)
	applied := map[string]bool{}
	var overwritten []ReviewEntry
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return res, fmt.Errorf("read archive: %w", err)
		}
		name := path.Clean(hdr.Name)
		parts := strings.Split(name, "/")
		if name == manifestName || len(parts) < 2 || parts[0] != "data" || hasDotDot(parts) {
			continue
		}
		secID := parts[1]
		if !want[secID] {
			continue
		}
		if len(parts) < 3 {
			continue
		}
		target, root := resolveTarget(s.section(secID), parts[2:])
		if target == "" {
			res.Skipped++
			continue
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, fs.FileMode(hdr.Mode).Perm()); err != nil {
				log.Printf("backup import: %v", err)
				res.Skipped++
				continue
			}
		case tar.TypeSymlink:
			// A failing MkdirAll skips the entry instead of aborting the
			// run, a planted symlink in the archive would fail it on purpose.
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil || !parentInside(filepath.Dir(target), root) {
				res.Skipped++
				continue
			}
			if info, err := os.Lstat(target); err == nil && info.Mode().IsRegular() {
				if err := os.Rename(target, target+preImportSuffix); err != nil {
					return res, err
				}
				overwritten = append(overwritten, newReviewEntry(target, secID))
			} else {
				os.Remove(target)
			}
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return res, err
			}
			res.Files++
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil || !parentInside(filepath.Dir(target), root) {
				res.Skipped++
				continue
			}
			overwrote, err := replaceFile(target, tr, fs.FileMode(hdr.Mode).Perm())
			if err != nil {
				return res, err
			}
			if overwrote {
				overwritten = append(overwritten, newReviewEntry(target, secID))
			}
			res.Files++
		default:
			res.Skipped++
			continue
		}
		applied[secID] = true
	}
	res.Sections = len(applied)
	res.Overwritten = len(overwritten)
	s.reviewAdd(overwritten)
	return res, nil
}

func newReviewEntry(target, section string) ReviewEntry {
	return ReviewEntry{ID: statefile.NewID(), Path: target, Section: section, CreatedAt: time.Now()}
}

// resolveTarget maps an archive path below a section onto the host. It
// returns the target and the source root the target must stay under, or an
// empty target for unknown sources.
func resolveTarget(sec *Section, parts []string) (string, string) {
	for _, src := range sec.Sources {
		if src.Name != parts[0] {
			continue
		}
		if len(parts) == 1 {
			return src.Path, filepath.Dir(src.Path)
		}
		return filepath.Join(src.Path, filepath.FromSlash(path.Join(parts[1:]...))), src.Path
	}
	return "", ""
}

func hasDotDot(parts []string) bool {
	for _, p := range parts {
		if p == ".." {
			return true
		}
	}
	return false
}

// parentInside reports whether the real path of dir stays at or below root,
// which blocks writes through symlinks a hostile archive planted earlier.
func parentInside(dir, root string) bool {
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	return realDir == realRoot || strings.HasPrefix(realDir, realRoot+string(filepath.Separator))
}

func writeFileAtomic(target string, r io.Reader, mode fs.FileMode) error {
	_, err := writeFile(target, r, mode, false)
	return err
}

// replaceFile writes the incoming file and, when it replaces a differing
// regular file, first parks the old version as <target>.dc-pre-import. It
// reports whether such a copy was made.
func replaceFile(target string, r io.Reader, mode fs.FileMode) (bool, error) {
	return writeFile(target, r, mode, true)
}

func writeFile(target string, r io.Reader, mode fs.FileMode, keepPrevious bool) (bool, error) {
	if mode == 0 {
		mode = 0o644
	}
	tmp, err := os.CreateTemp(filepath.Dir(target), ".dc-import-*")
	if err != nil {
		return false, err
	}
	_, copyErr := io.Copy(tmp, r)
	chmodErr := tmp.Chmod(mode)
	closeErr := tmp.Close()
	if copyErr != nil || chmodErr != nil || closeErr != nil {
		os.Remove(tmp.Name())
		if copyErr != nil {
			return false, copyErr
		}
		if chmodErr != nil {
			return false, chmodErr
		}
		return false, closeErr
	}
	overwrote := false
	if keepPrevious {
		if info, err := os.Lstat(target); err == nil && info.Mode().IsRegular() && !filesEqual(target, tmp.Name()) {
			if err := os.Rename(target, target+preImportSuffix); err != nil {
				os.Remove(tmp.Name())
				return false, err
			}
			overwrote = true
		}
	}
	if err := os.Rename(tmp.Name(), target); err != nil {
		os.Remove(tmp.Name())
		return false, err
	}
	return overwrote, nil
}

func filesEqual(a, b string) bool {
	ia, err := os.Stat(a)
	if err != nil {
		return false
	}
	ib, err := os.Stat(b)
	if err != nil || ia.Size() != ib.Size() {
		return false
	}
	fa, err := os.Open(a)
	if err != nil {
		return false
	}
	defer fa.Close()
	fb, err := os.Open(b)
	if err != nil {
		return false
	}
	defer fb.Close()
	bufA := make([]byte, 64*1024)
	bufB := make([]byte, 64*1024)
	for {
		na, errA := io.ReadFull(fa, bufA)
		nb, errB := io.ReadFull(fb, bufB)
		if !bytes.Equal(bufA[:na], bufB[:nb]) {
			return false
		}
		if errA != nil || errB != nil {
			return errors.Is(errA, io.EOF) == errors.Is(errB, io.EOF) && (errA == nil) == (errB == nil)
		}
	}
}
