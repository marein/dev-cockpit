package web

import (
	"net/http"

	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

// The create forms stand in a dialog as well as on their own page. The dialog
// asks for the very page a link points at, with modal=1, and gets the same form
// alone; the marker rides the query into the form's action and back out through
// the POST, like return and panel do. So the path a form posts to is still the
// path that rendered it, and the pages stay pages: a deep link, a login with a
// return and a browser without JS all still get them.
//
// What the marker changes is the answer. A dialog post keeps the dialog
// standing, so a refusal comes back as a message instead of a redirect that
// would throw the typed values away, and a create that worked hands the client
// the very destination the redirect would have gone to, flash and all.

// inFormModal reports whether this request came from the create dialog.
func inFormModal(c *gin.Context) bool {
	return c.Query("modal") == "1"
}

// formRefused answers a form the server turned down. The dialog shows the
// message over the untouched fields and a local caller wants the sentence and
// not a page, so both hear it as JSON; every other caller lands back on the
// form page with the flash it always got.
func (s *Server) formRefused(c *gin.Context, formPath, message string) {
	if inFormModal(c) || wantsJSON(c.Request) {
		c.JSON(http.StatusBadRequest, gin.H{"error": message})
		return
	}
	s.redirectWithFlash(c, formPath, "", message)
}

// createLanded answers a create that worked: the browser gets the redirect it
// always got, and a create out of the dialog gets the same destination as JSON,
// because the dialog navigates itself. The flash rides the session either way,
// so the page that follows renders it once.
func (s *Server) createLanded(c *gin.Context, location, message, errMsg string) {
	if !inFormModal(c) {
		if message == "" && errMsg == "" {
			c.Redirect(http.StatusSeeOther, location)
			return
		}
		s.redirectWithFlash(c, location, message, errMsg)
		return
	}
	sess := ginsessions.Default(c)
	setFlash(sess, message, errMsg)
	if err := sess.Save(); err != nil {
		s.renderError(c, http.StatusInternalServerError, "Something went wrong", saveSessionErrorMessage)
		return
	}
	c.JSON(http.StatusOK, gin.H{"location": location})
}

// createLandedProject is createLanded for a create that lands on the projects
// page, where the flash belongs to the new row and not to the top of the page
// (see redirectWithProjectFlash).
func (s *Server) createLandedProject(c *gin.Context, project, message string) {
	if !inFormModal(c) {
		s.redirectWithProjectFlash(c, project, message, "")
		return
	}
	if project == "" {
		s.createLanded(c, "/projects", message, "")
		return
	}
	ginsessions.Default(c).Set(flashProjectKey, project)
	s.createLanded(c, "/projects#project-"+project, message, "")
}
