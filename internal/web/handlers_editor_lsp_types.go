package web

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/editorintelligence"
	"github.com/marein/dev-cockpit/internal/project"
)

// maxTypeQueryLength bounds a type search query in bytes, far above any type
// name and the package words beside it.
const maxTypeQueryLength = 200

// handleEditorLSPTypes answers the types of the project whose names match
// the query, asked of the servers of the languages the project holds that
// list types. A project none of them covers answers covered false, which the
// palette words; an empty query asks nobody. A server that cannot answer
// travels as a status beside the types of the others, like a lookup's, and a
// cold or indexing one is waited for the way a lookup waits, which the
// palette shows from the indexing picture meanwhile.
func (s *Server) handleEditorLSPTypes(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	client := c.Query("client")
	if client == "" || len(client) > maxLSPClientIDLength {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A client id is required."})
		return
	}
	query := strings.TrimSpace(c.Query("q"))
	if len(query) > maxTypeQueryLength {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The query is too long."})
		return
	}
	modes := s.lspTypeModes(p)
	if len(modes) == 0 {
		c.JSON(http.StatusOK, gin.H{"covered": false, "types": []editorintelligence.TypeSymbol{}})
		return
	}
	if query == "" {
		c.JSON(http.StatusOK, gin.H{"covered": true, "types": []editorintelligence.TypeSymbol{}})
		return
	}
	res := s.intel.Types(c.Request.Context(), editorintelligence.TypesRequest{
		Client:      client,
		ProjectName: p.Name,
		ProjectRoot: p.Path,
		Query:       query,
		Profiles:    modes,
		Exclusions:  s.exclusions(),
	})
	c.JSON(http.StatusOK, gin.H{
		"covered":   true,
		"types":     res.Types,
		"truncated": res.Truncated,
		"statuses":  res.Statuses,
	})
}

// lspTypeModes answers the languages a type search of the project asks: the
// ones it holds files for whose servers list types, each with the way its
// server runs.
func (s *Server) lspTypeModes(p project.Project) []editorintelligence.WarmMode {
	found := s.lspProjectLanguages(p)
	for _, profile := range editorintelligence.Profiles() {
		if !profile.ListsTypes() {
			delete(found, profile.ID)
		}
	}
	return s.lspWarmModes(found)
}
