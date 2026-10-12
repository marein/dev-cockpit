package web

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/git"
)

// handleEditorGitCompare answers what differs between two revisions, the
// comparison the editor's revision diff panel lists: `?from=` and `?to=` are
// whatever git resolves (a branch, a tag, a hash, HEAD~3), both empty takes
// the repository's own suggestion, and `?mode=` picks the question, `since`
// (the default) for what the right side changed since it split from the left,
// git's three dots, `direct` for everything that differs between the two,
// git's two dots. `?worktree=1` puts the working copy on the right instead of
// `?to=`. It is a parameter of its own because a value of `to` is what
// somebody typed into the picker, and no typing may turn into the working
// copy; naming both is refused like a wrong name. A name git cannot resolve
// is the caller's mistake and answers a 400 naming the side; two revisions
// without shared history refuse the split question with a 409 in the
// package's words. It only reads.
func (s *Server) handleEditorGitCompare(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	worktree := c.Query("worktree")
	if worktree != "" && worktree != "1" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The working copy is asked for with worktree=1.", "side": "to"})
		return
	}
	if worktree == "1" && c.Query("to") != "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Compare with a revision or with the working copy, not both.", "side": "to"})
		return
	}
	got, err := git.New(p.Path).Compare(c.Request.Context(), git.CompareRequest{
		From:     c.Query("from"),
		To:       c.Query("to"),
		Since:    c.Query("mode") != "direct",
		Worktree: worktree == "1",
	})
	var refused *git.RevisionError
	switch {
	case errors.As(err, &refused):
		c.JSON(http.StatusBadRequest, gin.H{"error": refused.Error(), "side": refused.Side, "from": got.From, "to": got.To})
		return
	case errors.Is(err, git.ErrNoSplit):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "from": got.From, "to": got.To})
		return
	case err != nil:
		log.Printf("editor git compare %s: %v", p.Path, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "The revisions could not be compared."})
		return
	}
	c.JSON(http.StatusOK, got)
}
