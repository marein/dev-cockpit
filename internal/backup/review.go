package backup

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/local/dev-cockpit/internal/statefile"
)

// The import never destroys an existing file: a differing target is renamed
// to <path>.dc-pre-import before the archive version takes its place, and an
// entry lands in <state-dir>/import-review.json. The backup page lists the
// open entries until the user picks a side or merges, which removes the copy.

const preImportSuffix = ".dc-pre-import"

// mergeMaxSize caps the text merge view, larger files only offer keep or
// restore.
const mergeMaxSize = 256 * 1024

// ReviewEntry is one overwritten file awaiting a decision.
type ReviewEntry struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	Section   string    `json:"section"`
	CreatedAt time.Time `json:"createdAt"`
}

// PreImportPath returns the location of the saved previous version.
func (e ReviewEntry) PreImportPath() string { return e.Path + preImportSuffix }

// MergeView carries everything the merge page shows for one entry.
type MergeView struct {
	Entry    ReviewEntry
	Current  string
	Previous string
	Text     bool
	Diff     []DiffLine
	HasDiff  bool
}

func (s *Service) reviewFile() string { return filepath.Join(s.stateDir, "import-review.json") }

func (s *Service) loadReview() []ReviewEntry {
	var list []ReviewEntry
	statefile.Load(s.reviewFile(), &list)
	return list
}

func (s *Service) saveReview(list []ReviewEntry) {
	statefile.Save(s.reviewFile(), 0o644, list)
}

// ReviewList returns the open entries, dropping any whose pre-import copy is
// gone, so an externally cleaned up file heals the list.
func (s *Service) ReviewList() []ReviewEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.loadReview()
	kept := make([]ReviewEntry, 0, len(list))
	for _, e := range list {
		if _, err := os.Lstat(e.PreImportPath()); err != nil {
			continue
		}
		kept = append(kept, e)
	}
	if len(kept) != len(list) {
		s.saveReview(kept)
	}
	return kept
}

func (s *Service) reviewAdd(entries []ReviewEntry) {
	if len(entries) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.loadReview()
	for _, e := range entries {
		replaced := false
		for i := range list {
			if list[i].Path == e.Path {
				list[i] = e
				replaced = true
				break
			}
		}
		if !replaced {
			list = append(list, e)
		}
	}
	s.saveReview(list)
}

var errReviewResolved = errors.New("The file is already resolved.")

func takeReview(list []ReviewEntry, id string) (ReviewEntry, []ReviewEntry, bool) {
	for i, e := range list {
		if e.ID == id {
			return e, append(list[:i:i], list[i+1:]...), true
		}
	}
	return ReviewEntry{}, list, false
}

// ReviewKeep keeps the imported file and drops the previous copy.
func (s *Service) ReviewKeep(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, rest, ok := takeReview(s.loadReview(), id)
	if !ok {
		return errReviewResolved
	}
	if err := os.Remove(e.PreImportPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	s.saveReview(rest)
	return nil
}

// ReviewRestore puts the previous version back over the imported file.
func (s *Service) ReviewRestore(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, rest, ok := takeReview(s.loadReview(), id)
	if !ok {
		return errReviewResolved
	}
	if err := os.Rename(e.PreImportPath(), e.Path); err != nil {
		return err
	}
	s.saveReview(rest)
	return nil
}

// ReviewKeepAll resolves every open entry toward the imported files and
// returns how many copies were dropped.
func (s *Service) ReviewKeepAll() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.loadReview()
	count := 0
	for _, e := range list {
		if err := os.Remove(e.PreImportPath()); err == nil || errors.Is(err, fs.ErrNotExist) {
			count++
		}
	}
	s.saveReview(nil)
	return count
}

// CockpitPath reports whether path sits in the state dir. Those files feed
// the server process itself, changing one wants a restart.
func (s *Service) CockpitPath(path string) bool {
	return strings.HasPrefix(path, s.stateDir+string(filepath.Separator))
}

// ReviewNeedsRestart reports whether resolving this entry toward the
// previous or a merged version changes a cockpit file.
func (s *Service) ReviewNeedsRestart(id string) bool {
	e, ok := s.reviewByID(id)
	return ok && s.CockpitPath(e.Path)
}

func (s *Service) reviewByID(id string) (ReviewEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.loadReview() {
		if e.ID == id {
			return e, true
		}
	}
	return ReviewEntry{}, false
}

// Merge loads both versions of an entry for the merge page. Binary or large
// files come back with Text false, the page then only offers keep or
// restore.
func (s *Service) Merge(id string) (*MergeView, error) {
	e, ok := s.reviewByID(id)
	if !ok {
		return nil, errReviewResolved
	}
	prev, err := os.ReadFile(e.PreImportPath())
	if err != nil {
		return nil, errReviewResolved
	}
	cur, err := os.ReadFile(e.Path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	view := &MergeView{Entry: e}
	if !isText(prev) || !isText(cur) || len(prev) > mergeMaxSize || len(cur) > mergeMaxSize {
		return view, nil
	}
	view.Text = true
	view.Current = string(cur)
	view.Previous = string(prev)
	view.Diff, view.HasDiff = DiffLines(view.Previous, view.Current, 1000)
	return view, nil
}

// MergeSave writes the merged content over the imported file and resolves
// the entry.
func (s *Service) MergeSave(id, content string) error {
	e, ok := s.reviewByID(id)
	if !ok {
		return errReviewResolved
	}
	mode := fs.FileMode(0o644)
	if info, err := os.Lstat(e.Path); err == nil && info.Mode().IsRegular() {
		mode = info.Mode().Perm()
	}
	if err := writeFileAtomic(e.Path, strings.NewReader(content), mode); err != nil {
		return err
	}
	return s.ReviewKeep(id)
}

func isText(data []byte) bool {
	probe := data
	if len(probe) > 8000 {
		probe = probe[:8000]
		for !utf8.Valid(probe) && len(probe) > 7996 {
			probe = probe[:len(probe)-1]
		}
	}
	for _, b := range probe {
		if b == 0 {
			return false
		}
	}
	return utf8.Valid(probe)
}
