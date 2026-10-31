package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/filesystem"
)

func TestEditorLSPStructureRefusesWhatIsNoDocument(t *testing.T) {
	s, r := lspClassNameServer(t)
	r.POST("/projects/:name/editor/lsp/structure", s.handleEditorLSPStructure)
	for name, body := range map[string]string{
		"no json":                          `{`,
		"no client":                        `{"path":"a.php","content":"<?php"}`,
		"no path":                          `{"client":"c"}`,
		"a path out of the project":        `{"client":"c","path":"../x.php"}`,
		"an absolute path no server holds": `{"client":"c","path":"/etc/x.php"}`,
		"a document past the limit":        `{"client":"c","path":"a.php","content":"` + strings.Repeat("x", filesystem.MaxEditableBytes+1) + `"}`,
	} {
		code, answer := postLSP(t, r, "structure", body)
		if code != http.StatusBadRequest || answer["error"] == nil {
			t.Fatalf("%s: %d %v", name, code, answer)
		}
	}
}

func TestEditorLSPStructureAnswersAStatusWithoutAServer(t *testing.T) {
	s, r := lspClassNameServer(t)
	r.POST("/projects/:name/editor/lsp/structure", s.handleEditorLSPStructure)
	body := `{"client":"c","path":"src/a.php","content":"<?php\nclass A {}\n"}`
	code, answer := postLSP(t, r, "structure", body)
	if code != http.StatusOK || answer["available"] != false || answer["status"] != "unavailable" {
		t.Fatalf("no service: %d %v", code, answer)
	}

	s.settings.Set(editorLSPServerKey("php"), "off")
	code, answer = postLSP(t, r, "structure", body)
	if code != http.StatusOK || answer["available"] != false || answer["status"] != "disabled" {
		t.Fatalf("php switched off: %d %v", code, answer)
	}
}
