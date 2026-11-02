package web

import (
	"errors"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// repositorySearchHosts are the forge hosts the create form offers to clone
// from by a search of their repositories, none where the hosts or their
// tokens cannot be read.
func (s *Server) repositorySearchHosts() []forge.Host {
	if s.forge == nil {
		return nil
	}
	hosts, err := s.forge.SearchHosts()
	if err != nil {
		log.Printf("repository search hosts: %v", err)
		return nil
	}
	return hosts
}

// repositorySearchForm shapes the create form around the search of the host
// with that id. A host it no longer offers, which lost its token since the
// form was rendered, leaves the address field with what was filled in.
func (s *Server) repositorySearchForm(data *render.ProjectNewData, hosts []forge.Host, choice, id string) {
	data.Create = render.CreateClone
	for i, h := range hosts {
		if h.ID != id {
			continue
		}
		data.Create, data.Forge = choice, &data.Forges[i]
		data.Repository = data.Fill.CloneURL
		if b, ok := (forge.Hosts{h}).Resolve(data.Fill.CloneURL); ok {
			data.Repository = b.Path
		}
	}
}

// handleProjectRepositories answers the create form's search of one forge
// host: the repositories its token reaches that carry the words in q, or
// with scope=public the public ones whose name carries them. The two are
// asked apart, so the account's own stand at once and the public ones, which
// a forge may take seconds for, follow when they come. A public search that
// failed or was limited answers nothing and says nothing, the own one says
// why the host could not be searched.
func (s *Server) handleProjectRepositories(c *gin.Context) {
	if s.forge == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": forge.ErrNoSearch.Error()})
		return
	}
	public := c.Query("scope") == "public"
	search := s.forge.OwnRepositories
	if public {
		search = s.forge.PublicRepositories
	}
	found, err := search(c.Request.Context(), c.Query("host"), c.Query("q"))
	var refused *forge.SearchRefused
	switch {
	case errors.As(err, &refused):
		c.JSON(http.StatusBadRequest, gin.H{"error": refused.Reason})
	case errors.Is(err, forge.ErrNoSearch):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case err != nil && public:
		c.JSON(http.StatusOK, render.NewRepositoryChoices(nil))
	case err != nil:
		c.JSON(http.StatusOK, render.RepositorySearchFailed(err))
	default:
		c.JSON(http.StatusOK, render.NewRepositoryChoices(found))
	}
}
