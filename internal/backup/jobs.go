package backup

import (
	"errors"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/local/dev-cockpit/internal/statefile"
)

// Backups are created by a background job, one archive per run under
// <state-dir>/backups, so the page never blocks on a large export. The list
// lives in <state-dir>/backups.json; a job that dies with the process is
// marked failed on the next start. The backups directory is not a section
// source, so backups never travel inside a backup.

const (
	backupStateRunning = "running"
	backupStateDone    = "done"
	backupStateFailed  = "failed"
)

// StoredBackup is one archive in the backups directory.
type StoredBackup struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	Bytes     int64     `json:"bytes"`
	Sections  []string  `json:"sections"`
	Encrypted bool      `json:"encrypted"`
	State     string    `json:"state"`
	Error     string    `json:"error,omitempty"`
}

// Running reports whether the job behind this entry is still writing.
func (b StoredBackup) Running() bool { return b.State == backupStateRunning }

// Done reports whether the archive is complete and downloadable.
func (b StoredBackup) Done() bool { return b.State == backupStateDone }

func (s *Service) backupsDir() string  { return filepath.Join(s.stateDir, "backups") }
func (s *Service) backupsFile() string { return filepath.Join(s.stateDir, "backups.json") }

func (s *Service) loadBackups() []StoredBackup {
	var list []StoredBackup
	statefile.Load(s.backupsFile(), &list)
	return list
}

func (s *Service) saveBackups(list []StoredBackup) {
	statefile.Save(s.backupsFile(), 0o644, list)
}

// sweepJobs marks running entries from a previous process failed and drops
// their half written temp files. Called once at construction.
func (s *Service) sweepJobs() {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.loadBackups()
	changed := false
	for i := range list {
		if list[i].State == backupStateRunning {
			list[i].State = backupStateFailed
			list[i].Error = "interrupted by a restart"
			changed = true
		}
	}
	if changed {
		s.saveBackups(list)
	}
	if entries, err := os.ReadDir(s.backupsDir()); err == nil {
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".tmp") {
				os.Remove(filepath.Join(s.backupsDir(), e.Name()))
			}
		}
	}
}

// StartBackup validates the selection and launches the background job. done
// runs after the job finished, in both outcomes, with the final entry. A
// password is mandatory, backups only exist encrypted.
func (s *Service) StartBackup(ids []string, password string, done func(StoredBackup)) (StoredBackup, error) {
	known := s.Known(ids)
	if len(known) == 0 {
		return StoredBackup{}, errors.New("Select at least one section.")
	}
	if password == "" {
		return StoredBackup{}, errors.New("A password is required, backups are always encrypted.")
	}
	s.mu.Lock()
	for _, b := range s.loadBackups() {
		if b.State == backupStateRunning {
			s.mu.Unlock()
			return StoredBackup{}, errors.New("A backup is already running.")
		}
	}
	entry := StoredBackup{
		ID:        statefile.NewID(),
		CreatedAt: time.Now(),
		Sections:  known,
		Encrypted: true,
		State:     backupStateRunning,
	}
	entry.Name = "dev-cockpit-backup_" + entry.CreatedAt.Format("2006-01-02_150405") + "_" + entry.ID[:6] + ".dcbackup"
	if err := os.MkdirAll(s.backupsDir(), 0o700); err != nil {
		s.mu.Unlock()
		return StoredBackup{}, err
	}
	s.saveBackups(append(s.loadBackups(), entry))
	s.mu.Unlock()
	go s.runBackup(entry, known, password, done)
	return entry, nil
}

func (s *Service) runBackup(entry StoredBackup, ids []string, password string, done func(StoredBackup)) {
	err := s.writeBackup(entry, ids, password)
	var size int64
	if err == nil {
		if info, statErr := os.Stat(filepath.Join(s.backupsDir(), entry.Name)); statErr == nil {
			size = info.Size()
		}
	} else {
		log.Printf("backup %s failed: %v", entry.Name, err)
	}
	s.mu.Lock()
	list := s.loadBackups()
	for i := range list {
		if list[i].ID != entry.ID {
			continue
		}
		if err != nil {
			list[i].State = backupStateFailed
			list[i].Error = err.Error()
		} else {
			list[i].State = backupStateDone
			list[i].Bytes = size
		}
		entry = list[i]
	}
	s.saveBackups(list)
	s.mu.Unlock()
	if done != nil {
		done(entry)
	}
}

func (s *Service) writeBackup(entry StoredBackup, ids []string, password string) error {
	tmp := filepath.Join(s.backupsDir(), entry.ID+".tmp")
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	var w io.Writer = f
	var enc io.WriteCloser
	if password != "" {
		enc, err = NewEncryptWriter(f, password)
		if err != nil {
			f.Close()
			os.Remove(tmp)
			return err
		}
		w = enc
	}
	exportErr := s.Export(w, ids)
	if enc != nil && exportErr == nil {
		exportErr = enc.Close()
	}
	closeErr := f.Close()
	if exportErr != nil || closeErr != nil {
		os.Remove(tmp)
		if exportErr != nil {
			return exportErr
		}
		return closeErr
	}
	return os.Rename(tmp, filepath.Join(s.backupsDir(), entry.Name))
}

// ListBackups returns the stored backups newest first, dropping done entries
// whose archive disappeared externally.
func (s *Service) ListBackups() []StoredBackup {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.loadBackups()
	kept := make([]StoredBackup, 0, len(list))
	for _, b := range list {
		if b.State == backupStateDone {
			if _, err := os.Stat(filepath.Join(s.backupsDir(), b.Name)); err != nil {
				continue
			}
		}
		kept = append(kept, b)
	}
	if len(kept) != len(list) {
		s.saveBackups(kept)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].CreatedAt.After(kept[j].CreatedAt) })
	return kept
}

// LastFinished returns the newest entry that is no longer running, the one
// a just fired notification is about. ok is false while nothing finished
// yet.
func (s *Service) LastFinished() (StoredBackup, bool) {
	for _, b := range s.ListBackups() {
		if !b.Running() {
			return b, true
		}
	}
	return StoredBackup{}, false
}

// BackupFile returns the archive path and download name of a finished
// backup.
func (s *Service) BackupFile(id string) (string, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.loadBackups() {
		if b.ID != id {
			continue
		}
		if !b.Done() {
			return "", "", errors.New("The backup is not finished.")
		}
		return filepath.Join(s.backupsDir(), b.Name), b.Name, nil
	}
	return "", "", errors.New("The backup is gone.")
}

// DeleteBackup removes the archive and its entry and returns the removed
// entry. A running job cannot be deleted.
func (s *Service) DeleteBackup(id string) (StoredBackup, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.loadBackups()
	for i, b := range list {
		if b.ID != id {
			continue
		}
		if b.Running() {
			return StoredBackup{}, errors.New("The backup is still running.")
		}
		if err := os.Remove(filepath.Join(s.backupsDir(), b.Name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return StoredBackup{}, err
		}
		s.saveBackups(append(list[:i:i], list[i+1:]...))
		return b, nil
	}
	return StoredBackup{}, errors.New("The backup was already gone.")
}
