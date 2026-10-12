package web

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/git"
)

// handleEditorGitFile returns a file's text at a revision, HEAD without a
// ?rev=, which is the other side of the diff; the browser computes the diff
// itself, no route ever answers one. `?split=1` reads the file where HEAD
// split off from the revision instead, the merge base a merge request
// measures from, and names that commit as `split` when it is not the
// revision itself. A path that is not in the revision is a normal answer,
// that is what a new file looks like; a revision the repository cannot
// resolve is the caller's mistake and answers a 400 in the package's words,
// two histories without a split a 409. Binary and too large carry the same
// markers the plain read route uses, so the client shows the same "cannot
// edit this" as there.
func (s *Server) handleEditorGitFile(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	rel := c.Query("path")
	// The project root resolves fine and is not a file, so an empty path has to
	// be refused here: further down it is git that says no, and a missing
	// parameter would read as a repository that could not be asked.
	if rel == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A file path is required."})
		return
	}
	if _, err := filesystem.ResolveUnder(p.Path, rel); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	split := c.Query("split")
	if split != "" && split != "1" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The split is asked for with split=1."})
		return
	}
	repo := git.New(p.Path)
	answer := gin.H{"path": rel}
	rev := c.Query("rev")
	var err error
	if split == "1" {
		var at string
		if rev, at, err = repo.SplitPoint(c.Request.Context(), rev); err == nil && rev != at {
			answer["split"] = rev
		}
	}
	var content []byte
	exists := false
	if err == nil {
		content, exists, err = repo.FileAt(c.Request.Context(), rev, rel)
	}
	switch {
	case errors.Is(err, git.ErrRevision):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	case errors.Is(err, git.ErrNoSplit):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	case err != nil:
		log.Printf("editor git file %s: %v", p.Path, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "The file could not be read at that revision."})
		return
	}
	answer["exists"] = exists
	if !exists {
		answer["content"] = ""
		c.JSON(http.StatusOK, answer)
		return
	}
	// Binary and too large both mean "not something to diff", but they are not
	// the same sentence to read, so the reason travels with the answer. A blob
	// that fills git's own output cap counts as too large as well: git
	// truncates silently, and the head of a file diffed against the whole of it
	// claims everything past the cut was deleted. The cap sits below the edit
	// limit, so this is the answer for a revision between the two; a file that
	// happens to be exactly the cap is called too large with it, which is the
	// safe way to be wrong here.
	if err := filesystem.CheckEditableText(content); err != nil || len(content) >= git.MaxOutput {
		answer["binary"] = true
		answer["reason"] = "large"
		if errors.Is(err, filesystem.ErrBinary) {
			answer["reason"] = "binary"
		}
		answer["size"] = len(content)
		c.JSON(http.StatusOK, answer)
		return
	}
	answer["content"] = string(content)
	c.JSON(http.StatusOK, answer)
}
