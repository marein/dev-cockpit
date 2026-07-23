package web

import (
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/backup"
	"github.com/local/dev-cockpit/internal/web/render"
)

// downloadTokenPattern accepts the crypto.randomUUID tokens the dc-backup-busy
// element generates, echoed back as a cookie when the download stream starts
// so the page can drop its busy state.
var downloadTokenPattern = regexp.MustCompile(`^[0-9a-fA-F-]{8,64}$`)

func (s *Server) handleSettingsBackup(c *gin.Context) {
	s.backups.CleanupPending()
	data := render.SettingsBackupData{
		Page:   s.page(c, "Settings", "settings"),
		Groups: backupGroups(s.backups),
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
			rg.Sections = append(rg.Sections, render.BackupSection{
				ID: sec.ID, Label: sec.Label, Description: sec.Description, Available: sec.Available,
			})
		}
		out = append(out, rg)
	}
	return out
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
	for _, sec := range m.Sections {
		row := render.BackupImportSection{ID: sec.ID, Label: sec.Label, Files: sec.Files, Size: formatByteSize(sec.Bytes)}
		if view, ok := s.backups.Section(sec.ID); ok {
			row.Label = view.Label
			row.Description = view.Description
			row.Supported = true
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
		s.redirectWithAnchoredFlash(c, "/settings/backup", "settings-backup-review", fmt.Sprintf("Kept %d imported files.", count), "")
	default:
		s.redirectWithFlash(c, "/settings/backup", "", "Unknown form.")
	}
}

// backupReviewAction finishes a review resolution. Restoring or merging a
// cockpit file restarts the server, the process read those files at startup
// and would otherwise keep running on the replaced content.
func (s *Server) backupReviewAction(c *gin.Context, err error, message string, restart bool) {
	if err != nil {
		s.redirectWithAnchoredFlash(c, "/settings/backup", "settings-backup-review", "", err.Error())
		return
	}
	if restart && s.updater != nil {
		s.redirectWithAnchoredFlash(c, "/settings/backup", "settings-backup-review", message+" Dev Cockpit restarts now.", "")
		s.restartSoon("cockpit file resolved")
		return
	}
	if restart {
		message += " Restart Dev Cockpit to apply it."
	}
	s.redirectWithAnchoredFlash(c, "/settings/backup", "settings-backup-review", message, "")
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
		HasDiff:  view.HasDiff,
		Content:  view.Current,
		Previous: view.Previous,
		Restart:  s.backups.CockpitPath(view.Entry.Path),
	}
	for _, line := range view.Diff {
		data.Diff = append(data.Diff, render.BackupDiffLine{Kind: line.Kind, Text: line.Text})
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
		s.redirectWithAnchoredFlash(c, "/settings/backup", "settings-backup-import", "", "Please choose a backup file.")
		return
	}
	f, err := header.Open()
	if err != nil {
		s.redirectWithAnchoredFlash(c, "/settings/backup", "settings-backup-import", "", err.Error())
		return
	}
	defer f.Close()
	id, err := s.backups.SavePending(f, c.PostForm("password"))
	if err != nil {
		s.redirectWithAnchoredFlash(c, "/settings/backup", "settings-backup-import", "", err.Error())
		return
	}
	s.redirectWithAnchoredFlash(c, "/settings/backup?import="+id, "settings-backup-import", "", "")
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
	msg := fmt.Sprintf("Imported %d sections, %d files.", res.Sections, res.Files)
	if res.Skipped > 0 {
		msg += fmt.Sprintf(" %d entries were skipped.", res.Skipped)
	}
	if res.Overwritten > 0 {
		msg += fmt.Sprintf(" %d existing files differed and were kept as .dc-pre-import copies, review them below.", res.Overwritten)
	}
	if c.PostForm("restart") == "on" && s.updater != nil {
		s.redirectWithAnchoredFlash(c, "/settings/backup", "settings-backup-import", msg+" Dev Cockpit restarts now.", "")
		s.restartSoon("backup import applied")
		return
	}
	s.redirectWithAnchoredFlash(c, "/settings/backup", "settings-backup-import", msg+" Restart Dev Cockpit to apply everything.", "")
}

func (s *Server) handleSettingsBackupDownloadGet(c *gin.Context) {
	c.Redirect(http.StatusSeeOther, "/settings/backup")
}

// handleSettingsBackupDownload streams the archive. The path ends in
// /download, so the gzip middleware stays out of the way of the tar.gz.
func (s *Server) handleSettingsBackupDownload(c *gin.Context) {
	ids := s.backups.Known(c.PostFormArray("sections"))
	if len(ids) == 0 {
		s.redirectWithAnchoredFlash(c, "/settings/backup", "settings-backup-export", "", "Select at least one section.")
		return
	}
	password := c.PostForm("password")
	name := "dev-cockpit-backup_" + time.Now().Format("2006-01-02_150405")
	if password == "" {
		name += ".tar.gz"
	} else {
		name += ".dcbackup"
	}
	if token := c.PostForm("download_token"); downloadTokenPattern.MatchString(token) {
		c.SetSameSite(http.SameSiteLaxMode)
		c.SetCookie("dc-download", token, 120, "/settings/backup", "", requestIsSecure(c), false)
	}
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	c.Status(http.StatusOK)
	var w io.Writer = c.Writer
	var enc io.WriteCloser
	if password != "" {
		var err error
		enc, err = backup.NewEncryptWriter(c.Writer, password)
		if err != nil {
			log.Printf("backup export: %v", err)
			return
		}
		w = enc
	}
	if err := s.backups.Export(w, ids); err != nil {
		log.Printf("backup export: %v", err)
		return
	}
	if enc != nil {
		if err := enc.Close(); err != nil {
			log.Printf("backup export: %v", err)
		}
	}
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
