package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/shell"
	"github.com/marein/dev-cockpit/internal/tmux"
)

// The projects page deletes a shell from its chip through fetch and reads the
// answer as JSON, so a refusal has to come back the same way: a redirect plus
// a flash would land the page on a fresh /projects render that the chip code
// cannot read, and the row would fold again for nothing.
func TestShellDeleteAnswersARefusalAsJSONToAJSONCaller(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{shells: shell.NewShells(config.Config{}, tmux.New(), nil, nil)}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/shells/no.such.id/delete", nil)
	c.Request.Header.Set("Accept", "application/json")
	c.Params = gin.Params{{Key: "id", Value: "no.such.id"}}
	s.handleShellDelete(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want the refusal as %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
	var answer map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
		t.Fatalf("the refusal is not JSON: %v (%s)", err, rec.Body.String())
	}
	if answer["error"] == "" {
		t.Fatalf("the refusal carries no error field: %s", rec.Body.String())
	}
}
