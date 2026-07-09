package web

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/coder"
	"github.com/local/dev-cockpit/internal/terminal"
	"github.com/local/dev-cockpit/internal/web/render"
)

type coderCreateForm struct {
	Name              AlphaNumDashString `form:"name" binding:"required"`
	Project           string             `form:"project" binding:"required"`
	Coder             string             `form:"coder"`
	Agent             string             `form:"agent"`
	AutomaticApproval CheckboxBool       `form:"automatic_approval"`
}

type terminalInputItem struct {
	Prompt  string `json:"prompt"`
	Control string `json:"control"`
	Text    string `json:"text"`
	Paste   string `json:"paste"`
	Raw     string `json:"raw"`
}

type terminalInputBatch struct {
	Items []terminalInputItem `json:"items"`
}

// maxTerminalInputItems bounds one input flush so a single request can't pin the
// handler in a long run of tmux sends. Real typing bursts stay far below this.
const maxTerminalInputItems = 1024

type terminalResizeForm struct {
	Cols string `form:"cols" binding:"required"`
	Rows string `form:"rows" binding:"required"`
}

func (s *Server) handleCoderNew(c *gin.Context) {
	defaultPath := s.projects.DefaultPath()
	if name := strings.TrimSpace(c.Query("project")); name != "" {
		if p, err := s.projects.FindByName(name); err == nil {
			defaultPath = p.Path
		}
	}
	selected, err := s.coderFromRequest(c)
	if err != nil {
		selected = s.coders[0]
	}
	coders := make([]render.CoderChoice, 0, len(s.coders))
	for i := range s.coders {
		co := s.coders[i]
		defaultAgent := ""
		if agentID, err := co.Coder().AgentRepository().ValidateSelected(c.Query("agent")); err == nil {
			defaultAgent = agentID
		}
		coders = append(coders, render.CoderChoice{
			ID:           co.ID(),
			Agents:       co.Coder().AgentRepository().Options(),
			DefaultAgent: defaultAgent,
		})
	}
	c.HTML(http.StatusOK, "coders_new.gohtml", render.CoderNewData{
		Page:              s.page(c, "New Coder", "projects"),
		Projects:          s.projects.SelectablePaths(),
		DefaultPath:       defaultPath,
		Coders:            coders,
		SelectedCoder:     selected.ID(),
		AutomaticApproval: true,
		Return:            s.formReturn(c),
	})
}

func (s *Server) handleCoderAttach(c *gin.Context) {
	id := c.Param("id")
	co, running, err := s.resolveRunning(id)
	if err != nil {
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	files, err := co.Coder().SessionRepository().ListFiles(running.Identifier)
	if err != nil {
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	projectName := s.projects.ProjectNameFor(running.CWD)
	s.projects.Touch(projectName)
	s.notifier.MarkTargetRead(running.Identifier)
	c.HTML(http.StatusOK, "coder_attach.gohtml", render.CoderAttachData{
		Page:            s.page(c, pageTitle(running.Name, projectName), "projects"),
		Running:         running,
		Identifier:      running.Identifier,
		Coder:           co.ID(),
		ProjectName:     projectName,
		Files:           files,
		MaxUploadSizeMB: maxRequestBodyMegabytes(s.cfg.MaxRequestBodySize),
		Tabs:            s.terminalTabs(),
		StreamURL:       "/coders/" + running.Identifier + "/stream",
		ResizeURL:       "/coders/" + running.Identifier + "/resize",
		InputURL:        "/coders/" + running.Identifier + "/input",
	})
}

func (s *Server) handleCoderCreate(c *gin.Context) {
	var form coderCreateForm
	if !s.decodeForm(c, &form, "/coders/new") {
		return
	}
	co, err := s.coderFromRequest(c)
	if err != nil {
		s.redirectWithFlash(c, "/coders/new", "", err.Error())
		return
	}
	res, err := co.Start(
		form.Name.String(),
		form.Project,
		form.Agent,
		coder.StartOptions{
			AutomaticApproval: form.AutomaticApproval.Bool(),
		},
	)
	if err != nil {
		s.redirectWithFlash(c, "/coders/new", "", err.Error())
		return
	}
	c.Redirect(http.StatusSeeOther, "/coders/"+res.Identifier)
}

func (s *Server) handleCoderStop(c *gin.Context) {
	id := c.Param("id")
	co, running, err := s.resolveRunning(id)
	if err != nil {
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	project := s.projects.ProjectNameFor(running.CWD)
	name, err := co.Stop(id)
	if err != nil {
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	s.notifier.MarkTargetRead(id)
	s.redirectWithProjectFlash(c, project, "Coder \""+name+"\" stopped.", "")
}

func (s *Server) handleCoderFiles(c *gin.Context) {
	data, err := s.coderFilesData(c, c.Param("id"), "", "")
	if err != nil {
		c.HTML(http.StatusBadRequest, "coder_files_content.gohtml", render.CoderFilesData{
			Page:  s.page(c, "Files", "projects"),
			Error: err.Error(),
		})
		return
	}
	c.HTML(http.StatusOK, "coder_files_content.gohtml", data)
}

func (s *Server) handleCoderFileUpload(c *gin.Context) {
	id := c.Param("id")
	_, err := s.uploadCoderFiles(c, id)
	if err != nil {
		s.renderCoderFiles(c, id, http.StatusBadRequest, err.Error(), "")
		return
	}
	s.renderCoderFiles(c, id, http.StatusOK, "", "")
}

func (s *Server) uploadCoderFiles(c *gin.Context, id string) (int, error) {
	form, err := c.MultipartForm()
	if err != nil {
		return 0, fmt.Errorf("Please choose a file to upload.")
	}
	files := form.File["files"]
	if len(files) == 0 {
		return 0, fmt.Errorf("Please choose a file to upload.")
	}

	co, err := s.coderForSession(id)
	if err != nil {
		return 0, err
	}
	for _, header := range files {
		src, err := header.Open()
		if err != nil {
			return 0, err
		}
		_, saveErr := co.Coder().SessionRepository().SaveFile(id, header.Filename, src)
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

func (s *Server) handleCoderFileDownload(c *gin.Context) {
	id := c.Param("id")
	co, err := s.coderForSession(id)
	if err != nil {
		s.redirectWithFlash(c, "/coders/"+id, "", err.Error())
		return
	}
	file, err := co.Coder().SessionRepository().OpenFile(id, c.Query("name"))
	if err != nil {
		s.redirectWithFlash(c, "/coders/"+id, "", err.Error())
		return
	}
	defer file.Close()

	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": file.Name})
	c.DataFromReader(http.StatusOK, file.Size, "application/octet-stream", file, map[string]string{
		"Content-Disposition": disposition,
	})
}

func (s *Server) handleCoderFileDelete(c *gin.Context) {
	id := c.Param("id")
	co, err := s.coderForSession(id)
	if err != nil {
		s.renderCoderFiles(c, id, http.StatusBadRequest, err.Error(), "")
		return
	}
	file, err := co.Coder().SessionRepository().DeleteFile(id, c.PostForm("name"))
	if err != nil {
		s.renderCoderFiles(c, id, http.StatusBadRequest, err.Error(), "")
		return
	}
	s.renderCoderFiles(c, id, http.StatusOK, "", "File \""+file.Name+"\" deleted.")
}

func (s *Server) renderCoderFiles(c *gin.Context, id string, status int, errorMessage, message string) {
	data, err := s.coderFilesData(c, id, errorMessage, message)
	if err != nil {
		data = render.CoderFilesData{
			Page:  s.page(c, "Files", "projects"),
			Error: err.Error(),
		}
		status = http.StatusBadRequest
	}
	c.HTML(status, "coder_files_content.gohtml", data)
}

func (s *Server) coderFilesData(c *gin.Context, id, errorMessage, message string) (render.CoderFilesData, error) {
	co, err := s.coderForSession(id)
	if err != nil {
		return render.CoderFilesData{}, err
	}
	files, err := co.Coder().SessionRepository().ListFiles(id)
	if err != nil {
		return render.CoderFilesData{}, err
	}
	return render.CoderFilesData{
		Page:            s.page(c, "Files", "projects"),
		Identifier:      id,
		Files:           files,
		MaxUploadSizeMB: maxRequestBodyMegabytes(s.cfg.MaxRequestBodySize),
		Error:           errorMessage,
		Message:         message,
	}, nil
}

// pageTitle composes the browser title for a terminal page as "name - project",
// falling back to just the name when the working directory sits outside any project.
func pageTitle(name, projectName string) string {
	if projectName == "" {
		return name
	}
	return name + " - " + projectName
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

func (s *Server) handleCoderInput(c *gin.Context) {
	id := c.Param("id")
	var batch terminalInputBatch
	if err := c.ShouldBindJSON(&batch); err != nil {
		c.String(http.StatusBadRequest, "Invalid input.")
		return
	}
	if len(batch.Items) > maxTerminalInputItems {
		c.String(http.StatusRequestEntityTooLarge, "Too many input items.")
		return
	}
	items := make([]terminal.Input, len(batch.Items))
	for i, item := range batch.Items {
		items[i] = terminal.Input(item)
	}
	co, err := s.coderForInput(id)
	if err != nil {
		if errors.Is(err, coder.ErrNotRunning) {
			c.String(http.StatusGone, err.Error())
			return
		}
		c.String(http.StatusBadRequest, err.Error())
		return
	}
	if err := co.Send(id, items); err != nil {
		if errors.Is(err, coder.ErrNotRunning) {
			c.String(http.StatusGone, err.Error())
			return
		}
		c.String(http.StatusBadRequest, err.Error())
		return
	}
	c.String(http.StatusOK, "OK")
}

func (s *Server) handleCoderResize(c *gin.Context) {
	id := c.Param("id")
	var form terminalResizeForm
	if err := c.Bind(&form); err != nil {
		return
	}
	co, err := s.coderForInput(id)
	if err == nil {
		err = co.Resize(id, form.Cols, form.Rows)
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": userFacingError(c, err)})
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *Server) handleCoderResume(c *gin.Context) {
	id := c.Param("id")
	// Already running (e.g. resumed in another tab): just go to its page.
	if _, running, err := s.resolveRunning(id); err == nil {
		c.Redirect(http.StatusSeeOther, "/coders/"+running.Identifier)
		return
	}
	co, _, err := s.resolveResumable(id)
	if err != nil {
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	stored, err := co.Resume(id)
	if err != nil {
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	c.Redirect(http.StatusSeeOther, "/coders/"+stored.SessionID)
}

func (s *Server) handleCoderDelete(c *gin.Context) {
	id := c.Param("id")
	co, _, err := s.resolveResumable(id)
	if err != nil {
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	stored, err := co.DeleteResumable(id)
	if err != nil {
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	s.redirectWithProjectFlash(c, s.projects.ProjectNameFor(stored.CWD), "Inactive coder \""+stored.Name+"\" deleted.", "")
}
