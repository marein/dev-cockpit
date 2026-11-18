package web

import (
	"bytes"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/browser"
	"github.com/marein/dev-cockpit/internal/browserextension"
)

const settingsBrowserExtensionDownloadPath = "/settings/browser/extension/:file"

// The zip carries the cockpit's own picker, console catch, drawing, context
// format and filter: one source for what a pick reads, how a send words it
// and how a menu ranks.
func (s *Server) handleBrowserExtensionDownload(c *gin.Context) {
	name, zipped := strings.CutSuffix(c.Param("file"), ".zip")
	if !zipped {
		s.handleNotFound(c)
		return
	}
	shared, err := browserExtensionShared()
	if err != nil {
		c.String(http.StatusInternalServerError, "The extension could not be built.")
		return
	}
	var out bytes.Buffer
	if err := browserextension.Zip(&out, name, shared); err != nil {
		if errors.Is(err, browserextension.ErrBrowser) {
			s.handleNotFound(c)
			return
		}
		c.String(http.StatusInternalServerError, "The extension could not be built.")
		return
	}
	c.Header("Content-Disposition", `attachment; filename="dev-cockpit-browser-extension-`+name+"-"+browserextension.Version+`.zip"`)
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "application/zip", out.Bytes())
}

func browserExtensionShared() (map[string][]byte, error) {
	shared := map[string][]byte{"pick.js": browser.PickScript(), "console.js": browser.ConsoleScript(), "draw.js": browser.DrawScript()}
	for _, name := range []string{"browser-context.js", "filter.js"} {
		data, err := staticAssets.ReadFile("static/js/dc/" + name)
		if err != nil {
			return nil, err
		}
		shared[name] = data
	}
	return shared, nil
}
