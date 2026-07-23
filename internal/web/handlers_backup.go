package web

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/backup"
	"github.com/local/dev-cockpit/internal/eventbus"
	"github.com/local/dev-cockpit/internal/notify"
	"github.com/local/dev-cockpit/internal/web/render"
)

func (s *Server) backupRows() []render.BackupRow {
	var rows []render.BackupRow
	for _, b := range s.backups.ListBackups() {
		rows = append(rows, render.BackupRow{
			ID:       b.ID,
			Name:     b.Name,
			Created:  b.CreatedAt.Format("2006-01-02 15:04"),
			Size:     formatByteSize(b.Bytes),
			Sections: len(b.Sections),
			Running:  b.Running(),
			Done:     b.Done(),
			Error:    b.Error,
		})
	}
	return rows
}

// handleSettingsBackupList renders just the backup list fragment, without
// touching the flash, so dc-backup-list can refresh live without eating a
// pending create or delete flash on a racing redirect.
func (s *Server) handleSettingsBackupList(c *gin.Context) {
	c.HTML(http.StatusOK, "settings_backup_list.gohtml", render.BackupListData{
		Backups:   s.backupRows(),
		CSRFToken: s.csrfToken(c),
	})
}

func (s *Server) handleSettingsBackup(c *gin.Context) {
	s.backups.CleanupPending()
	// Being here is seeing the backup outcome, so the backup notification
	// reads itself, like opening an attach page does for a terminal.
	s.notifier.MarkTargetRead(notify.BackupTarget)
	data := render.SettingsBackupData{
		Page:    s.page(c, "Settings", "settings"),
		Backups: s.backupRows(),
	}
	for _, e := range s.backups.ReviewList() {
		data.Review = append(data.Review, render.BackupReviewRow{
			ID: e.ID, Path: e.Path, CSRFToken: data.CSRFToken, Restart: s.backups.CockpitPath(e.Path),
		})
	}
	if len(data.Review) > 12 {
		data.ReviewMoreCount = len(data.Review) - 12
	}
	if id := c.Query("import"); id != "" {
		imp, err := s.backupImport(id)
		if err != nil {
			data.ImportError = err.Error()
		} else {
			data.Import = imp
		}
	}
	data.Tab = "export"
	if c.Query("tab") == "import" || c.Query("import") != "" ||
		data.FlashProject == "settings-backup-import" || data.FlashProject == "settings-backup-review" {
		data.Tab = "import"
	}
	c.HTML(http.StatusOK, "settings_backup.gohtml", data)
}

func backupGroups(svc *backup.Service) []render.BackupGroup {
	groups := svc.Groups()
	out := make([]render.BackupGroup, 0, len(groups))
	for _, g := range groups {
		rg := render.BackupGroup{Label: g.Label}
		for _, sec := range g.Sections {
			labels := make([]string, 0, len(sec.Requires))
			for _, id := range sec.Requires {
				if dep, ok := svc.Section(id); ok {
					labels = append(labels, dep.Label)
				}
			}
			row := render.BackupSection{
				ID: sec.ID, Label: sec.Label, Description: sec.Description, Available: sec.Available,
				Requires:       strings.Join(sec.Requires, " "),
				RequiresLabels: strings.Join(labels, ", "),
			}
			if sec.ID == "dotfiles" {
				row.Detail = strings.Join(svc.HomeDotfiles(), ", ")
			}
			rg.Sections = append(rg.Sections, row)
		}
		out = append(out, rg)
	}
	return out
}

func (s *Server) handleSettingsBackupNew(c *gin.Context) {
	c.HTML(http.StatusOK, "settings_backup_new.gohtml", render.SettingsBackupNewData{
		Page:   s.page(c, "Settings", "settings"),
		Groups: backupGroups(s.backups),
	})
}

// handleSettingsBackupCreate starts the background backup job and returns to
// the list at once. The finished job raises a notification and a backups
// event, so open lists flip from running to done without a reload.
func (s *Server) handleSettingsBackupCreate(c *gin.Context) {
	_, err := s.backups.StartBackup(c.PostFormArray("sections"), c.PostForm("password"), func(backup.StoredBackup) {
		s.notifier.Add(notify.BackupTarget)
		s.publishBackups()
	})
	if err != nil {
		s.redirectWithFlash(c, "/settings/backup/new", "", err.Error())
		return
	}
	s.publishBackups()
	s.redirectWithAnchoredFlash(c, "/settings/backup", "settings-backup-export", "The backup is being created in the background.", "")
}

// publishBackups tells open backup lists to re-pull their fragment.
func (s *Server) publishBackups() {
	s.bus.Publish(eventbus.Event{Type: "backups"})
}

func (s *Server) backupImport(id string) (*render.BackupImport, error) {
	m, err := s.backups.Inspect(id)
	if err != nil {
		return nil, err
	}
	imp := &render.BackupImport{
		Token:   id,
		Host:    m.Host,
		Version: m.AppVersion,
	}
	if !m.CreatedAt.IsZero() {
		imp.Created = m.CreatedAt.Local().Format("2006-01-02 15:04")
	}
	present := map[string]bool{}
	for _, sec := range m.Sections {
		present[sec.ID] = true
	}
	for _, sec := range m.Sections {
		row := render.BackupImportSection{ID: sec.ID, Label: sec.Label, Files: sec.Files, Size: formatByteSize(sec.Bytes)}
		if view, ok := s.backups.Section(sec.ID); ok {
			row.Label = view.Label
			row.Description = view.Description
			row.Supported = true
			// Only enforce dependencies the archive actually carries, a
			// required section absent from the file cannot be selected.
			var ids, labels []string
			for _, dep := range view.Requires {
				if !present[dep] {
					continue
				}
				if depView, ok := s.backups.Section(dep); ok {
					ids = append(ids, dep)
					labels = append(labels, depView.Label)
				}
			}
			row.Requires = strings.Join(ids, " ")
			row.RequiresLabels = strings.Join(labels, ", ")
		}
		imp.Sections = append(imp.Sections, row)
	}
	return imp, nil
}

// handleSettingsBackupSave dispatches the backup page forms on their hidden
// form field, so every form POSTs to the path that renders it.
func (s *Server) handleSettingsBackupSave(c *gin.Context) {
	switch c.PostForm("form") {
	case "inspect":
		s.backupInspect(c)
	case "apply":
		s.backupApply(c)
	case "discard":
		s.backups.Discard(c.PostForm("token"))
		s.redirectWithAnchoredFlash(c, "/settings/backup", "settings-backup-import", "Import discarded.", "")
	case "review-keep":
		s.backupReviewAction(c, s.backups.ReviewKeep(c.PostForm("id")), "Kept the imported file.", false)
	case "review-restore":
		id := c.PostForm("id")
		restart := s.backups.ReviewNeedsRestart(id)
		s.backupReviewAction(c, s.backups.ReviewRestore(id), "Restored the previous file.", restart)
	case "review-keep-all":
		count := s.backups.ReviewKeepAll()
		s.redirectWithAnchoredFlash(c, "/settings/backup", "settings-backup-import", fmt.Sprintf("Kept %d imported files. All files are resolved.", count), "")
	case "backup-delete":
		deleted, err := s.backups.DeleteBackup(c.PostForm("id"))
		if err != nil {
			s.redirectWithAnchoredFlash(c, "/settings/backup", "settings-backup-export", "", err.Error())
			return
		}
		s.publishBackups()
		s.redirectWithAnchoredFlash(c, "/settings/backup", "settings-backup-export", fmt.Sprintf("Backup %q deleted.", deleted.Name), "")
	default:
		s.redirectWithFlash(c, "/settings/backup", "", "Unknown form.")
	}
}

// backupReviewAction finishes a review resolution. Restoring or merging a
// cockpit file restarts the server, the process read those files at startup
// and would otherwise keep running on the replaced content. The flash stays
// anchored at the review section while entries remain; resolving the last
// one anchors it at the import pane, the review section is gone then and a
// flash anchored to a missing section would render nowhere.
func (s *Server) backupReviewAction(c *gin.Context, err error, message string, restart bool) {
	anchor := "settings-backup-review"
	if s.backups.PendingReviewCount() == 0 {
		anchor = "settings-backup-import"
	}
	if err != nil {
		s.redirectWithAnchoredFlash(c, "/settings/backup", anchor, "", err.Error())
		return
	}
	if anchor == "settings-backup-import" {
		message += " All files are resolved."
	}
	if restart && s.updater != nil {
		s.redirectWithAnchoredFlash(c, "/settings/backup", anchor, message+" Dev Cockpit restarts now.", "")
		s.restartSoon("cockpit file resolved")
		return
	}
	if restart {
		message += " Restart Dev Cockpit to apply it."
	}
	s.redirectWithAnchoredFlash(c, "/settings/backup", anchor, message, "")
}

// restartSoon re-execs the server once the response reached the browser,
// same pattern as the self-update apply.
func (s *Server) restartSoon(reason string) {
	go func() {
		time.Sleep(500 * time.Millisecond)
		log.Printf("%s, restarting", reason)
		if err := s.updater.Restart(); err != nil {
			log.Printf("restart failed: %v", err)
		}
	}()
}

func (s *Server) handleSettingsBackupMerge(c *gin.Context) {
	view, err := s.backups.Merge(c.Query("id"))
	if err != nil {
		s.redirectWithAnchoredFlash(c, "/settings/backup", "settings-backup-review", "", err.Error())
		return
	}
	data := render.BackupMergeData{
		Page:     s.page(c, "Settings", "settings"),
		ID:       view.Entry.ID,
		FilePath: view.Entry.Path,
		Text:     view.Text,
		Content:  view.Current,
		Previous: view.Previous,
		Restart:  s.backups.CockpitPath(view.Entry.Path),
	}
	c.HTML(http.StatusOK, "settings_backup_merge.gohtml", data)
}

// handleSettingsBackupMergeSave dispatches the merge page forms on their
// hidden form field, all POSTing to the path that renders the page.
func (s *Server) handleSettingsBackupMergeSave(c *gin.Context) {
	id := c.PostForm("id")
	restart := s.backups.ReviewNeedsRestart(id)
	switch c.PostForm("form") {
	case "save":
		content := strings.ReplaceAll(c.PostForm("content"), "\r\n", "\n")
		s.backupReviewAction(c, s.backups.MergeSave(id, content), "Merged file saved.", restart)
	case "keep":
		s.backupReviewAction(c, s.backups.ReviewKeep(id), "Kept the imported file.", false)
	case "restore":
		s.backupReviewAction(c, s.backups.ReviewRestore(id), "Restored the previous file.", restart)
	default:
		s.redirectWithFlash(c, "/settings/backup", "", "Unknown form.")
	}
}

func (s *Server) backupInspect(c *gin.Context) {
	header, err := c.FormFile("archive")
	if err != nil {
		s.anchoredFlashResponse(c, "/settings/backup", "settings-backup-import", "", "Please choose a backup file.")
		return
	}
	f, err := header.Open()
	if err != nil {
		s.anchoredFlashResponse(c, "/settings/backup", "settings-backup-import", "", err.Error())
		return
	}
	defer f.Close()
	id, err := s.backups.SavePending(f, c.PostForm("password"))
	if err != nil {
		s.anchoredFlashResponse(c, "/settings/backup", "settings-backup-import", "", err.Error())
		return
	}
	s.anchoredFlashResponse(c, "/settings/backup?import="+id, "settings-backup-import", "", "")
}

func (s *Server) backupApply(c *gin.Context) {
	token := c.PostForm("token")
	ids := c.PostFormArray("sections")
	if len(s.backups.Known(ids)) == 0 {
		s.redirectWithAnchoredFlash(c, "/settings/backup?import="+token, "settings-backup-import", "", "Select at least one section.")
		return
	}
	res, err := s.backups.Apply(token, ids)
	if err != nil {
		s.redirectWithAnchoredFlash(c, "/settings/backup?import="+token, "settings-backup-import", "", err.Error())
		return
	}
	s.backups.Discard(token)
	// A clean import flashes at the top of the import pane; an import that
	// left clashes flashes at the review section, where the work now sits.
	anchor := "settings-backup-import"
	msg := fmt.Sprintf("Imported %d sections, %d files.", res.Sections, res.Files)
	if res.Skipped > 0 {
		msg += fmt.Sprintf(" %d entries were skipped.", res.Skipped)
	}
	if res.Overwritten > 0 {
		anchor = "settings-backup-review"
		msg += fmt.Sprintf(" %d existing files differed and were kept as .dc-pre-import copies, resolve them below.", res.Overwritten)
	}
	if c.PostForm("restart") == "on" && s.updater != nil {
		s.redirectWithAnchoredFlash(c, "/settings/backup", anchor, msg+" Dev Cockpit restarts now.", "")
		s.restartSoon("backup import applied")
		return
	}
	s.redirectWithAnchoredFlash(c, "/settings/backup", anchor, msg+" Restart Dev Cockpit to apply everything.", "")
}

// handleSettingsBackupDownload serves a finished archive. The path ends in
// /download, so the gzip middleware stays out of the way of the tar.gz.
func (s *Server) handleSettingsBackupDownload(c *gin.Context) {
	path, name, err := s.backups.BackupFile(c.Query("id"))
	if err != nil {
		s.redirectWithAnchoredFlash(c, "/settings/backup", "settings-backup-export", "", err.Error())
		return
	}
	c.FileAttachment(path, name)
}

func formatByteSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
