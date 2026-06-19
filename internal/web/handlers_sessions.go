package web

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/session"
	"github.com/local/dev-cockpit/internal/web/render"
)

type sessionCreateForm struct {
	Name              AlphaNumDashString `form:"name" binding:"required"`
	Project           string             `form:"project" binding:"required"`
	Agent             string             `form:"agent"`
	RemoteControl     CheckboxBool       `form:"remote_control"`
	AutomaticApproval CheckboxBool       `form:"automatic_approval"`
}

type sessionInputItem struct {
	Prompt  string `json:"prompt"`
	Control string `json:"control"`
	Text    string `json:"text"`
	Paste   string `json:"paste"`
	Raw     string `json:"raw"`
}

type sessionInputBatch struct {
	Items []sessionInputItem `json:"items"`
}

// maxSessionInputItems bounds one input flush so a single request can't pin the
// handler in a long run of tmux sends. Real typing bursts stay far below this.
const maxSessionInputItems = 1024

type sessionResizeForm struct {
	Cols string `form:"cols" binding:"required"`
	Rows string `form:"rows" binding:"required"`
}

func (s *Server) handleSessionsList(c *gin.Context) {
	snap := s.sessions.Snapshot()
	c.HTML(http.StatusOK, "sessions_list.gohtml", render.SessionsData{
		Page:     s.page(c, "Sessions", "sessions"),
		Snapshot: snap,
	})
}

func (s *Server) handleSessionNew(c *gin.Context) {
	defaultPath := s.projects.DefaultPath()
	defaultAgent := ""
	if projectName := strings.TrimSpace(c.Query("project")); projectName != "" {
		if p, err := s.projects.FindByName(projectName); err == nil {
			defaultPath = p.Path
		}
	}
	if agentID, err := s.provider.AgentRepository().ValidateSelected(c.Query("agent")); err == nil {
		defaultAgent = agentID
	}
	c.HTML(http.StatusOK, "sessions_new.gohtml", render.SessionNewData{
		Page:              s.page(c, "New Session", "sessions"),
		Projects:          s.projects.SelectablePaths(),
		DefaultPath:       defaultPath,
		Agents:            s.provider.AgentRepository().Options(),
		DefaultAgent:      defaultAgent,
		RemoteControl:     true,
		AutomaticApproval: true,
	})
}

func (s *Server) handleSessionAttach(c *gin.Context) {
	id := c.Param("id")
	running, err := s.sessions.ResolveRunning(id)
	if err != nil {
		s.redirectWithFlash(c, "/sessions", "", err.Error())
		return
	}
	files, err := s.provider.SessionRepository().ListFiles(running.Identifier)
	if err != nil {
		s.redirectWithFlash(c, "/sessions", "", err.Error())
		return
	}
	c.HTML(http.StatusOK, "session_attach.gohtml", render.SessionAttachData{
		Page:              s.page(c, running.Name, "sessions"),
		Session:           running,
		SessionIdentifier: running.Identifier,
		Files:             files,
		MaxUploadSizeMB:   maxRequestBodyMegabytes(s.cfg.MaxRequestBodySize),
		StreamURL:         "/sessions/" + running.Identifier + "/stream",
		ResizeURL:         "/sessions/" + running.Identifier + "/resize",
		InputURL:          "/sessions/" + running.Identifier + "/input",
	})
}

func (s *Server) handleSessionCreate(c *gin.Context) {
	var form sessionCreateForm
	if !s.decodeForm(c, &form, "/sessions/new") {
		return
	}
	res, err := s.sessions.Start(
		form.Name.String(),
		form.Project,
		form.Agent,
		session.StartOptions{
			RemoteControl:     form.RemoteControl.Bool(),
			AutomaticApproval: form.AutomaticApproval.Bool(),
		},
	)
	if err != nil {
		s.redirectWithFlash(c, "/sessions/new", "", err.Error())
		return
	}
	c.Redirect(http.StatusSeeOther, "/sessions/"+res.Identifier)
}

func (s *Server) handleSessionStop(c *gin.Context) {
	id := c.Param("id")
	name, err := s.sessions.Stop(id)
	if err != nil {
		s.redirectWithFlash(c, "/sessions", "", err.Error())
		return
	}
	s.redirectWithFlash(c, "/sessions", "Session \""+name+"\" stopped.", "")
}

func (s *Server) handleSessionFiles(c *gin.Context) {
	data, err := s.sessionFilesData(c, c.Param("id"), "", "")
	if err != nil {
		c.HTML(http.StatusBadRequest, "session_files_content.gohtml", render.SessionFilesData{
			Page:  s.page(c, "Files", "sessions"),
			Error: err.Error(),
		})
		return
	}
	c.HTML(http.StatusOK, "session_files_content.gohtml", data)
}

func (s *Server) handleSessionFileUpload(c *gin.Context) {
	id := c.Param("id")
	_, err := s.uploadSessionFiles(c, id)
	if err != nil {
		s.renderSessionFiles(c, id, http.StatusBadRequest, err.Error(), "")
		return
	}
	s.renderSessionFiles(c, id, http.StatusOK, "", "")
}

func (s *Server) uploadSessionFiles(c *gin.Context, id string) (int, error) {
	form, err := c.MultipartForm()
	if err != nil {
		return 0, fmt.Errorf("Please choose a file to upload.")
	}
	files := form.File["files"]
	if len(files) == 0 {
		return 0, fmt.Errorf("Please choose a file to upload.")
	}

	for _, header := range files {
		src, err := header.Open()
		if err != nil {
			return 0, err
		}
		_, saveErr := s.provider.SessionRepository().SaveFile(id, header.Filename, src)
		closeErr := src.Close()
		if saveErr != nil {
			return 0, saveErr
		}
		if closeErr != nil {
			return 0, closeErr
		}
	}

	return len(files), nil
}

func (s *Server) handleSessionFileDownload(c *gin.Context) {
	id := c.Param("id")
	file, err := s.provider.SessionRepository().OpenFile(id, c.Query("name"))
	if err != nil {
		s.redirectWithFlash(c, "/sessions/"+id, "", err.Error())
		return
	}
	defer file.Close()

	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": file.Name})
	c.DataFromReader(http.StatusOK, file.Size, "application/octet-stream", file, map[string]string{
		"Content-Disposition": disposition,
	})
}

func (s *Server) handleSessionFileDelete(c *gin.Context) {
	id := c.Param("id")
	file, err := s.provider.SessionRepository().DeleteFile(id, c.PostForm("name"))
	if err != nil {
		s.renderSessionFiles(c, id, http.StatusBadRequest, err.Error(), "")
		return
	}
	s.renderSessionFiles(c, id, http.StatusOK, "", "File \""+file.Name+"\" deleted.")
}

func (s *Server) renderSessionFiles(c *gin.Context, id string, status int, errorMessage, message string) {
	data, err := s.sessionFilesData(c, id, errorMessage, message)
	if err != nil {
		data = render.SessionFilesData{
			Page:  s.page(c, "Files", "sessions"),
			Error: err.Error(),
		}
		status = http.StatusBadRequest
	}
	c.HTML(status, "session_files_content.gohtml", data)
}

func (s *Server) sessionFilesData(c *gin.Context, id, errorMessage, message string) (render.SessionFilesData, error) {
	files, err := s.provider.SessionRepository().ListFiles(id)
	if err != nil {
		return render.SessionFilesData{}, err
	}
	return render.SessionFilesData{
		Page:              s.page(c, "Files", "sessions"),
		SessionIdentifier: id,
		Files:             files,
		MaxUploadSizeMB:   maxRequestBodyMegabytes(s.cfg.MaxRequestBodySize),
		Error:             errorMessage,
		Message:           message,
	}, nil
}

func maxRequestBodyMegabytes(size int64) string {
	if size <= 0 {
		return ""
	}
	mb := size / (1024 * 1024)
	if mb <= 0 {
		return "1"
	}
	return fmt.Sprintf("%d", mb)
}

func (s *Server) handleSessionInput(c *gin.Context) {
	id := c.Param("id")
	var batch sessionInputBatch
	if err := c.ShouldBindJSON(&batch); err != nil {
		c.String(http.StatusBadRequest, "Invalid input.")
		return
	}
	if len(batch.Items) > maxSessionInputItems {
		c.String(http.StatusRequestEntityTooLarge, "Too many input items.")
		return
	}
	items := make([]session.Input, len(batch.Items))
	for i, item := range batch.Items {
		items[i] = session.Input(item)
	}
	if err := s.sessions.Send(id, items); err != nil {
		if errors.Is(err, session.ErrNoActiveSession) {
			c.String(http.StatusGone, err.Error())
			return
		}
		c.String(http.StatusBadRequest, err.Error())
		return
	}
	c.String(http.StatusOK, "OK")
}

func (s *Server) handleSessionResize(c *gin.Context) {
	id := c.Param("id")
	var form sessionResizeForm
	if err := c.Bind(&form); err != nil {
		return
	}
	err := s.sessions.Resize(id, form.Cols, form.Rows)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleResumableResume(c *gin.Context) {
	id := c.Param("id")
	stored, err := s.sessions.Resume(id)
	if err != nil {
		s.redirectWithFlash(c, "/sessions", "", err.Error())
		return
	}
	c.Redirect(http.StatusSeeOther, "/sessions/"+stored.SessionID)
}

func (s *Server) handleResumableDelete(c *gin.Context) {
	id := c.Param("id")
	stored, err := s.sessions.DeleteResumable(id)
	if err != nil {
		s.redirectWithFlash(c, "/sessions", "", err.Error())
		return
	}
	s.redirectWithFlash(c, "/sessions", "Inactive session \""+stored.Name+"\" deleted.", "")
}
