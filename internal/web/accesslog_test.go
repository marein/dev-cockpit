package web

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

const logToken = "Ab3_dE-fGhIjKlMnOpQrStUvWxYz0123456789aBcDe"

// A token leaves every line it stands in, raw or URL encoded, the case and
// the depth of the encoding whatever, and the rest of the line stays.
func TestRedactShareTokensCutsTheTokenInEveryForm(t *testing.T) {
	for line, want := range map[string]string{
		`GET "/share/` + logToken + `/projects/shop/editor"`:                                            `GET "/share/…/projects/shop/editor"`,
		`GET "/share/` + logToken + `/docker/links?path=%2Fshare%2F` + logToken + `%2Fprojects%2Fshop"`: `GET "/share/…/docker/links?path=%2Fshare%2F…%2Fprojects%2Fshop"`,
		`path=%2fshare%2f` + logToken:                                                                   `path=%2fshare%2f…`,
		`return=%252Fshare%252F` + logToken + `%252Fx`:                                                  `return=%252Fshare%252F…%252Fx`,
		`Referer: https://eax.home.arpa:3000/share/` + logToken + `/terminals`:                          `Referer: https://eax.home.arpa:3000/share/…/terminals`,
		`back=/SHARE/` + logToken:                                                                       `back=/SHARE/…`,
		`blame /root/projects/shop share/` + logToken + `: missing`:                                     `blame /root/projects/shop share/…: missing`,
		`GET "/settings/sharing/abc" and "/projects/share-it/editor"`:                                   `GET "/settings/sharing/abc" and "/projects/share-it/editor"`,
	} {
		if got := string(redactShareTokens([]byte(line))); got != want {
			t.Fatalf("redacted %q\n to %q\nwant %q", line, got, want)
		}
	}
}

// The access log of a guest's status line request holds neither the token of
// its address nor the one its query would name.
func TestTheAccessLogHoldsNoShareToken(t *testing.T) {
	var out bytes.Buffer
	r := gin.New()
	r.Use(gin.LoggerWithWriter(redactingWriter{&out}))
	r.GET("/share/:token/docker/links", func(c *gin.Context) { c.Status(http.StatusOK) })
	target := "/share/" + logToken + "/docker/links?path=%2Fshare%2F" + logToken + "%2Fprojects%2Fshop%2Feditor"
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil))
	if line := out.String(); strings.Contains(line, logToken) || !strings.Contains(line, "/share/…/docker/links?path=%2Fshare%2F…%2Fprojects") {
		t.Fatalf("the access log reads %q", line)
	}
}

// A line of the standard logger, a handler's or the HTTP server's, holds no
// token either, and a second server wraps the logger no further.
func TestTheStandardLogHoldsNoShareToken(t *testing.T) {
	saved := log.Writer()
	defer log.SetOutput(saved)
	var out bytes.Buffer
	log.SetOutput(&out)
	redactStandardLog()
	redactStandardLog()
	if w, ok := log.Writer().(redactingWriter); !ok || w.out != &out {
		t.Fatalf("the standard log writes to %T", log.Writer())
	}
	log.Printf("editor git blame /root/projects/shop share/%s: open %%2Fshare%%2F%s", logToken, logToken)
	if line := out.String(); strings.Contains(line, logToken) {
		t.Fatalf("the standard log reads %q", line)
	}
}
