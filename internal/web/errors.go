package web

import (
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/web/render"
)

// userFacingError converts an internal error into a message safe to return to
// the browser. Filesystem/OS errors embed absolute server paths and syscall
// detail, so they are mapped to a clean message (and the raw error is logged
// server-side); the curated errors.New messages from internal/filesystem are
// already user-facing and pass through unchanged.
func userFacingError(c *gin.Context, err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "File or folder not found."
	case errors.Is(err, fs.ErrPermission):
		return "Permission denied."
	}
	var (
		pathErr *fs.PathError
		linkErr *os.LinkError
	)
	if errors.As(err, &pathErr) || errors.As(err, &linkErr) {
		log.Printf("%s %s: %v", c.Request.Method, c.Request.URL.Path, err)
		return "The operation could not be completed."
	}
	return err.Error()
}

// renderError responds with a {"error": message} body when the client wants
// JSON (see wantsJSON) and otherwise renders the styled HTML error page. It then
// aborts the request, and never panics, so it is safe to call from panic
// recovery.
func (s *Server) renderError(c *gin.Context, status int, heading, message string) {
	if c.Writer.Written() {
		c.Abort()
		return
	}
	if wantsJSON(c.Request) {
		c.JSON(status, gin.H{"error": message})
	} else {
		c.HTML(status, "error.gohtml", render.ErrorPage{
			Title:   strconv.Itoa(status) + " " + heading,
			Status:  status,
			Heading: heading,
			Message: message,
		})
	}
	c.Abort()
}

// wantsJSON reports whether the client wants a machine-readable body instead of
// the HTML error page: either it asked for JSON (Accept: application/json, as
// the editor's fetch calls do) or it is an XHR (X-Requested-With, as the file
// upload). Such callers surface the {"error": message} themselves.
func wantsJSON(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "application/json") ||
		r.Header.Get("X-Requested-With") == "XMLHttpRequest"
}

func (s *Server) handleNotFound(c *gin.Context) {
	s.renderError(c, http.StatusNotFound, "Page not found",
		"The page you're looking for doesn't exist or has moved.")
}

func (s *Server) handleMethodNotAllowed(c *gin.Context) {
	s.renderError(c, http.StatusMethodNotAllowed, "Method not allowed",
		"That action isn't available on this page.")
}

// recoveryHandler turns an unrecovered panic into a 500 error page instead of a
// blank connection drop. Signature matches gin.CustomRecovery.
func (s *Server) recoveryHandler(c *gin.Context, _ any) {
	s.renderError(c, http.StatusInternalServerError, "Something went wrong",
		"An unexpected error occurred. Please try again.")
}
